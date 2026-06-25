package messages_search

import (
	"testing"
)

func TestSingleSearchAllHit_File(t *testing.T) {
	tp := payloadTypeFile
	doc := Doc{
		MessageID:  100,
		MessageSeq: 9,
		From:       "u1",
		Timestamp:  1717000000,
		Payload: &Payload{
			Type: &tp,
			File: &FilePayload{Name: "a.pdf", Ext: "pdf", URL: "http://x"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, nil)
	if got.ResultType != "file" {
		t.Errorf("result_type: got %q", got.ResultType)
	}
	if got.File == nil || got.File.FileName != "a.pdf" {
		t.Fatalf("file should be populated: %+v", got.File)
	}
	if got.Message != nil {
		t.Errorf("message should be nil for file result: %+v", got.Message)
	}
	if got.SortedAt != got.File.SentAt {
		t.Errorf("sorted_at must mirror inner sent_at: got %q vs %q", got.SortedAt, got.File.SentAt)
	}
}

func TestSingleSearchAllHit_TextMessage(t *testing.T) {
	tp := payloadTypeText
	doc := Doc{
		MessageID:  101,
		MessageSeq: 10,
		From:       "u2",
		Timestamp:  1717000001,
		Payload: &Payload{
			Type: &tp,
			Text: &TextPayload{Content: "hello"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	hl := map[string][]string{"payload.text.content": {"<mark>hello</mark>"}}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, hl)
	if got.ResultType != "message" {
		t.Errorf("result_type: got %q", got.ResultType)
	}
	if got.Message == nil || got.Message.Snippet == "" {
		t.Fatalf("message + snippet expected: %+v", got.Message)
	}
	if got.Message.MessageKind != "text" {
		t.Errorf("text kind: got %q", got.Message.MessageKind)
	}
	if got.File != nil {
		t.Errorf("file should be nil for message result")
	}
}

func TestSingleSearchAllHit_ForwardKeepsMessageType(t *testing.T) {
	tp := payloadTypeMergeForward
	doc := Doc{
		MessageID: 102,
		Timestamp: 100,
		Payload: &Payload{
			Type:         &tp,
			MergeForward: &MergeForwardPayload{ChildCount: 4},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, nil)
	if got.ResultType != "message" {
		t.Errorf("forward must be 'message' (file is type=8 only): got %q", got.ResultType)
	}
	if got.Message == nil || got.Message.MessageKind != "forward" {
		t.Errorf("forward kind: %+v", got.Message)
	}
	if got.Message.OuterPreview == nil || got.Message.OuterPreview.ChildCount != 4 {
		t.Errorf("outer_preview: %+v", got.Message.OuterPreview)
	}
}

// Rich-text (payload.type=14) keeps result_type=message — it is rendered as a
// message, not a file — and folds into the existing "text" kind so the wire
// contract stays at the two-value enum {text, forward}. Snippet falls back to
// the indexer's plain projection (payload.richText.searchText) when no
// highlight was attached (empty-keyword browse).
func TestSingleSearchAllHit_RichTextKeepsMessageType(t *testing.T) {
	tp := payloadTypeRichText
	doc := Doc{
		MessageID:  103,
		MessageSeq: 11,
		From:       "u3",
		Timestamp:  1717000002,
		Payload: &Payload{
			Type:     &tp,
			RichText: &RichTextPayload{SearchText: "富文本搜索 命中预览"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	// Keyword path: highlight on richText.searchText wins via pickSnippet.
	hl := map[string][]string{"payload.richText.searchText": {"富文本<mark>搜索</mark>"}}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, hl)
	if got.ResultType != "message" {
		t.Errorf("richtext must be 'message': got %q", got.ResultType)
	}
	if got.Message == nil {
		t.Fatalf("message must be populated for richtext")
	}
	if got.Message.MessageKind != "text" {
		t.Errorf("richtext kind must fold into text: got %q", got.Message.MessageKind)
	}
	if got.Message.Snippet != "富文本<mark>搜索</mark>" {
		t.Errorf("richtext keyword snippet: got %q", got.Message.Snippet)
	}
	// Empty-keyword browse path: no highlight → fall back to raw richText.
	got2 := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, nil)
	if got2.Message.Snippet != "富文本搜索 命中预览" {
		t.Errorf("richtext browse fallback snippet: got %q", got2.Message.Snippet)
	}
	if got.File != nil {
		t.Errorf("file should be nil for richtext result")
	}
}
