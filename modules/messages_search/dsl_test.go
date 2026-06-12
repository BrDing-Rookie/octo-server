package messages_search

import (
	"encoding/json"
	"strings"
	"testing"
)

// extractDSL serialises a query for asserting structural shape in tests.
func extractDSL(t *testing.T, q interface {
	Source() (any, error)
}) map[string]any {
	t.Helper()
	src, err := q.Source()
	if err != nil {
		t.Fatalf("Source(): %v", err)
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestBuildSearchMessagesDSL_Shape(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "groupNo",
		Keyword:     "hello",
		Filters: SearchFilters{
			SenderIDs: []string{"u1", "u2"},
		},
	}
	q := buildSearchMessagesDSL(req, "groupNo")
	dsl := extractDSL(t, q.(interface {
		Source() (any, error)
	}))
	js, _ := json.Marshal(dsl)
	body := string(js)
	for _, want := range []string{
		`"multi_match"`,
		`"hello"`,
		`"payload.text.content^3"`,
		`"payload.mergeForward.msgs.searchText"`,
		`"channelId":"groupNo"`,
		`"revoked":true`,
		`"payload.type":99`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("DSL missing %q in:\n%s", want, body)
		}
	}
}

func TestBuildSearchMediaDSL_FiltersTypes(t *testing.T) {
	req := SearchMediaReq{ChannelType: channelTypeGroup, ChannelID: "g"}
	q := buildSearchMediaDSL(req, "g")
	dsl := extractDSL(t, q.(interface {
		Source() (any, error)
	}))
	js, _ := json.Marshal(dsl)
	body := string(js)
	if !strings.Contains(body, `"payload.type":[2,5]`) && !strings.Contains(body, `"payload.type":[2, 5]`) {
		t.Errorf("media DSL should filter on payload.type [2,5]:\n%s", body)
	}
	if strings.Contains(body, "multi_match") {
		t.Errorf("media DSL must not include multi_match")
	}
}

func TestBuildSearchFilesDSL_NoKeywordSkipsMultiMatch(t *testing.T) {
	req := SearchFilesReq{ChannelType: channelTypeGroup, ChannelID: "g"}
	q := buildSearchFilesDSL(req, "g")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Errorf("file DSL with empty keyword must not include multi_match:\n%s", body)
	}
	if !strings.Contains(body, `"payload.type":8`) {
		t.Errorf("file DSL must filter type=8:\n%s", body)
	}
}

func TestBuildSearchFilesDSL_KeywordIncludesMultiMatch(t *testing.T) {
	req := SearchFilesReq{ChannelType: channelTypeGroup, ChannelID: "g", Keyword: "report"}
	q := buildSearchFilesDSL(req, "g")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if !strings.Contains(body, `"multi_match"`) {
		t.Errorf("file DSL with keyword should include multi_match:\n%s", body)
	}
	if !strings.Contains(body, "payload.file.name^2") {
		t.Errorf("file DSL with keyword should target payload.file.name^2:\n%s", body)
	}
}

func TestBuildSearchAllDSL_TypeFilter(t *testing.T) {
	req := SearchMessagesReq{ChannelType: channelTypeGroup, ChannelID: "g", Keyword: "k"}
	q := buildSearchAllDSL(req, "g")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	for _, want := range []string{
		`"payload.type":[1,8,11]`,
		`"minimum_should_match":"1"`,
		`"payload.text.content^3"`,
		`"payload.file.name^2"`,
	} {
		if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, ",", ", ")) {
			t.Errorf("search_all DSL missing %q in:\n%s", want, body)
		}
	}
}

func TestExtractSortValues(t *testing.T) {
	ts, msg, score := extractSortValues([]any{float64(1717000000), float64(9876543210)}, false)
	if ts != 1717000000 || msg != 9876543210 {
		t.Fatalf("got ts=%d msgID=%d", ts, msg)
	}
	if score != nil {
		t.Fatalf("time_* sort should yield score=nil, got %v", *score)
	}
	if ts, msg, score := extractSortValues(nil, false); ts != 0 || msg != 0 || score != nil {
		t.Fatalf("nil sort should give zeros, got %d %d %v", ts, msg, score)
	}
}

func TestExtractSortValues_Relevance(t *testing.T) {
	// relevance sort emits [timestamp, _score, messageId]
	ts, msg, score := extractSortValues([]any{float64(1717000000), float64(12.5), float64(9876543210)}, true)
	if ts != 1717000000 || msg != 9876543210 {
		t.Fatalf("got ts=%d msgID=%d", ts, msg)
	}
	if score == nil || *score != 12.5 {
		t.Fatalf("expected score=12.5, got %v", score)
	}
	// short sort under relevance returns zeros + nil
	if ts, msg, score := extractSortValues([]any{float64(1), float64(2)}, true); ts != 0 || msg != 0 || score != nil {
		t.Fatalf("short relevance sort should give zeros, got %d %d %v", ts, msg, score)
	}
}
