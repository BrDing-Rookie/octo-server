package messages_search

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/olivere/elastic"
	"go.uber.org/zap"
)

// MediaHit is the response shape per A doc §2.2.
//
// duration_ms is omitted on image hits via `omitempty`; thumb_url is required
// per spec but we still tag it omitempty so historical rows missing the field
// don't blow up the wire shape. Spec §2.2 lists no channel_id (the request
// channel is implicit) and no sender_avatar_url (waterfall card layout has no
// avatar surface) — both are intentionally absent.
type MediaHit struct {
	MessageID   string `json:"message_id"`
	MessageSeq  int64  `json:"message_seq"`
	MediaKind   string `json:"media_kind"`
	ThumbURL    string `json:"thumb_url,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	SenderID    string `json:"sender_id"`
	SenderName  string `json:"sender_name,omitempty"`
	SentAt      string `json:"sent_at"`
	MonthBucket string `json:"month_bucket"`
}

func init() {
	registerRoute(func(h *Handler, g *wkhttp.RouterGroup) {
		g.POST("/_search_media", h.searchMedia)
	})
}

// searchMedia is POST /v1/messages/_search_media.
func (h *Handler) searchMedia(c *wkhttp.Context) {
	var req SearchMediaReq
	if err := c.BindJSON(&req); err != nil {
		respondValidation(c, "body", "invalid JSON")
		return
	}
	req.Keyword = strings.TrimSpace(req.Keyword)
	loginUID := c.GetLoginUID()

	if !validateKeywordMustBeEmpty(c, req.Keyword) {
		return
	}
	pageSize, ok := validateBase(c, h.cfg, req.ChannelType, req.ChannelID, req.Sort, req.Cursor, req.Filters, req.PageSize, false)
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
	dsl := buildSearchMediaDSL(req, normID)

	svc := client.Search().
		Index(h.cfg.OSReadAlias).
		Routing(normID).
		Query(dsl).
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
		h.Warn("OS search media failed", zap.Error(err))
		if responder := classifyOSError(err); responder != nil {
			responder(c)
		} else {
			respondInternal(c)
		}
		return
	}

	items := h.buildMediaHits(ctx, result, req, loginUID)
	hasMore, nextCursor := h.computeCursorPagination(result, pageSize, req.Sort)

	recordAudit(c, "search_media", req.ChannelType, req.ChannelID, "", len(items))
	c.Response(envelope(items, hasMore, nextCursor))
}

func buildSearchMediaDSL(req SearchMediaReq, normChannelID string) elastic.Query {
	b := elastic.NewBoolQuery()
	applyChannelAndRevoked(b, normChannelID)
	b.Filter(elastic.NewTermsQuery("payload.type", payloadTypeImage, payloadTypeVideo))
	addCommonFilters(b, req.Filters)
	return b
}

func (h *Handler) buildMediaHits(ctx context.Context, result *elastic.SearchResult, req SearchMediaReq, loginUID string) []MediaHit {
	if result == nil || result.Hits == nil {
		return []MediaHit{}
	}
	items := make([]MediaHit, 0, len(result.Hits.Hits))
	senderIDs := make([]string, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var doc Doc
		if err := json.Unmarshal(rawSource(hit.Source), &doc); err != nil {
			h.Warn("messages_search: bad media _source skipped", zap.Error(err))
			continue
		}
		items = append(items, h.singleMediaHit(doc, req))
		senderIDs = append(senderIDs, doc.From)
	}

	if len(items) == 0 {
		return items
	}
	// MediaHit has no sender_avatar_url surface (spec §2.2) — only Names is
	// applied; Avatars from the join is intentionally discarded.
	join := h.senderJoin(ctx, uniqUIDs(senderIDs), req.ChannelType, req.ChannelID)
	for i := range items {
		items[i].SenderName = join.Names[items[i].SenderID]
	}
	return items
}

// singleMediaHit projects a single Doc into a MediaHit. Extracted so unit
// tests can drive the field mapping without standing up a full search loop.
func (h *Handler) singleMediaHit(doc Doc, req SearchMediaReq) MediaHit {
	mh := MediaHit{
		MessageID:   strconv.FormatInt(doc.MessageID, 10),
		MessageSeq:  int64(doc.MessageSeq),
		SenderID:    doc.From,
		SentAt:      msToRFC3339(doc.Timestamp),
		MonthBucket: monthBucket(doc.Timestamp),
	}
	switch payloadType(doc.Payload) {
	case payloadTypeImage:
		if img := imagePayloadOf(doc.Payload); img != nil {
			mh.MediaKind = "image"
			mh.Width = img.Width
			mh.Height = img.Height
		}
	case payloadTypeVideo:
		if vid := videoPayloadOf(doc.Payload); vid != nil {
			mh.MediaKind = "video"
			mh.ThumbURL = vid.Cover
			mh.Width = vid.Width
			mh.Height = vid.Height
			mh.DurationMs = int64(vid.Second) * 1000
		}
	}
	return mh
}

func imagePayloadOf(p *Payload) *ImagePayload {
	if p == nil {
		return nil
	}
	return p.Image
}

func videoPayloadOf(p *Payload) *VideoPayload {
	if p == nil {
		return nil
	}
	return p.Video
}
