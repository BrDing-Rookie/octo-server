package messages_search

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/olivere/elastic"
	"go.uber.org/zap"
)

// SearchAllHit is the response shape per A doc §2.4. Either Message or File
// is populated based on ResultType; SortedAt is a flat copy of the inner
// sent_at to make pagination deterministic across mixed result types.
type SearchAllHit struct {
	ResultType string      `json:"result_type"`
	SortedAt   string      `json:"sorted_at"`
	Message    *MessageHit `json:"message,omitempty"`
	File       *FileHit    `json:"file,omitempty"`
}

func init() {
	registerRoute(func(h *Handler, g *wkhttp.RouterGroup) {
		g.POST("/_search_all", h.searchAll)
	})
}

// searchAll is POST /v1/messages/_search_all.
func (h *Handler) searchAll(c *wkhttp.Context) {
	var req SearchAllReq
	if err := c.BindJSON(&req); err != nil {
		respondValidation(c, "body", "invalid JSON")
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	loginUID := c.GetLoginUID()

	if !validateKeywordRequired(c, req.Keyword) {
		return
	}
	pageSize, ok := validateBase(c, h.cfg, req.ChannelType, req.ChannelID, req.Sort, req.Cursor, req.Filters, req.PageSize, true)
	if !ok {
		return
	}

	client, err := ESClient(h.cfg)
	if err != nil {
		h.Error("ESClient init failed", zap.Error(err))
		respondUpstream(c)
		return
	}

	normID := normalizedChannelID(req.ChannelType, req.ChannelID, loginUID)
	dsl := buildSearchAllDSL(req, normID)

	svc := client.Search().
		Index(h.cfg.OSReadAlias).
		Routing(normID).
		Query(dsl).
		Highlight(buildSearchAllHighlight()).
		Size(pageSize).
		TrackTotalHits(false)
	svc = applySort(svc, req.Sort)

	if req.Cursor != "" {
		ts, msgID, score, err := decodeCursor(h.cfg, req.Cursor)
		if err != nil {
			respondValidation(c, "cursor", "malformed")
			return
		}
		if req.Sort == "relevance" {
			if score == nil {
				respondValidation(c, "cursor", "stale_cursor_format")
				return
			}
			svc = svc.SearchAfter(ts, *score, msgID)
		} else {
			svc = svc.SearchAfter(ts, msgID)
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.cfg.Timeout)
	defer cancel()
	result, err := svc.Do(ctx)
	if err != nil {
		h.Warn("OS search_all failed", zap.Error(err))
		if responder := classifyOSError(err); responder != nil {
			responder(c)
		} else {
			respondInternal(c)
		}
		return
	}

	items := h.buildSearchAllHits(ctx, result, req, loginUID)
	hasMore, nextCursor := h.computeCursorPagination(result, pageSize, req.Sort)

	recordAudit(c, "search_all", req.ChannelType, req.ChannelID, req.Keyword, len(items))
	c.Response(envelope(items, hasMore, nextCursor))
}

func buildSearchAllDSL(req SearchAllReq, normChannelID string) elastic.Query {
	b := elastic.NewBoolQuery()
	applyChannelAndRevoked(b, normChannelID)
	b.Filter(elastic.NewTermsQuery("payload.type",
		payloadTypeText,
		payloadTypeFile,
		payloadTypeMergeForward,
	))
	addCommonFilters(b, req.Filters)
	b.Should(
		elastic.NewMultiMatchQuery(req.Keyword,
			"payload.text.content^3",
			"payload.mergeForward.msgs.searchText",
		),
		elastic.NewMultiMatchQuery(req.Keyword,
			"payload.file.name^2",
			"payload.file.caption",
		),
	)
	b.MinimumShouldMatch("1")
	return b
}

func buildSearchAllHighlight() *elastic.Highlight {
	return elastic.NewHighlight().
		PreTags("<mark>").PostTags("</mark>").
		FragmentSize(120).
		NumOfFragments(1).
		Field("payload.text.content").
		Field("payload.mergeForward.msgs.searchText").
		Field("payload.file.name")
}

func (h *Handler) buildSearchAllHits(ctx context.Context, result *elastic.SearchResult, req SearchAllReq, loginUID string) []SearchAllHit {
	if result == nil || result.Hits == nil {
		return []SearchAllHit{}
	}
	items := make([]SearchAllHit, 0, len(result.Hits.Hits))
	senderIDs := make([]string, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var doc Doc
		if err := json.Unmarshal(rawSource(hit.Source), &doc); err != nil {
			h.Warn("messages_search: bad search_all _source skipped", zap.Error(err))
			continue
		}
		hl := map[string][]string(hit.Highlight)
		entry := h.singleSearchAllHit(doc, req, hl)
		items = append(items, entry)
		senderIDs = append(senderIDs, doc.From)
	}

	if len(items) == 0 {
		return items
	}
	join := h.senderJoin(ctx, uniqUIDs(senderIDs), req.ChannelType, req.ChannelID)
	for i := range items {
		switch items[i].ResultType {
		case "message":
			if items[i].Message != nil {
				items[i].Message.SenderName = join.Names[items[i].Message.SenderID]
				items[i].Message.SenderAvatarURL = join.Avatars[items[i].Message.SenderID]
			}
		case "file":
			if items[i].File != nil {
				items[i].File.SenderName = join.Names[items[i].File.SenderID]
				items[i].File.SenderAvatarURL = join.Avatars[items[i].File.SenderID]
			}
		}
	}
	return items
}

// singleSearchAllHit projects a single Doc into the result_type-tagged shape
// _search_all returns. Extracted so unit tests can drive the dispatcher
// without hitting OS.
func (h *Handler) singleSearchAllHit(doc Doc, req SearchAllReq, hl map[string][]string) SearchAllHit {
	entry := SearchAllHit{SortedAt: msToRFC3339(doc.Timestamp)}
	if payloadType(doc.Payload) == payloadTypeFile {
		fh := h.singleFileHit(doc)
		entry.ResultType = "file"
		entry.File = &fh
		entry.SortedAt = fh.SentAt
	} else {
		mh := h.singleMessageHit(doc, req.ChannelID, hl)
		entry.ResultType = "message"
		entry.Message = &mh
		entry.SortedAt = mh.SentAt
	}
	return entry
}
