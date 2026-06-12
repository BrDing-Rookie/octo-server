package messages_search

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/gin-gonic/gin"
)

// stubAuthzGroupSvc fakes group.IService for the membership gate. The embedded
// interface is nil so any method other than ExistMemberActive panics on call —
// proving checkChannelAccess uses the fail-closed active-member variant only.
type stubAuthzGroupSvc struct {
	group.IService
	activeMembers map[string]bool // groupNo → caller is active member
	err           error
	calls         int
	gotGroupNo    string
	gotUID        string
}

func (s *stubAuthzGroupSvc) ExistMemberActive(groupNo string, uid string) (bool, error) {
	s.calls++
	s.gotGroupNo = groupNo
	s.gotUID = uid
	if s.err != nil {
		return false, s.err
	}
	return s.activeMembers[groupNo], nil
}

func newAuthzCtx(t *testing.T) (*wkhttp.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(rec)
	gc.Request = httptest.NewRequest("POST", "/v1/messages/_search", nil)
	return &wkhttp.Context{Context: gc}, rec
}

func newAuthzHandler(gSvc group.IService) *Handler {
	return &Handler{
		Log:          log.NewTLog("messages_search-authz-test"),
		cfg:          SearchConfig{},
		groupService: gSvc,
	}
}

// TestCheckChannelAccess_P2PAlwaysAllowed — p2p search is safe by construction
// (fakeChannelID embeds loginUID) and must not consult the group service.
func TestCheckChannelAccess_P2PAlwaysAllowed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypePerson, "peer-uid", "me") {
		t.Fatalf("p2p must always pass the gate")
	}
	if gSvc.calls != 0 {
		t.Fatalf("p2p must not call ExistMemberActive, got %d calls", gSvc.calls)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("no response should be written on allow, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_GroupMemberAllowed — active members search their group.
func TestCheckChannelAccess_GroupMemberAllowed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"G1": true}}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("active member must pass the gate")
	}
	if gSvc.gotGroupNo != "G1" || gSvc.gotUID != "me" {
		t.Fatalf("membership checked with wrong identity: group=%q uid=%q", gSvc.gotGroupNo, gSvc.gotUID)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("no response should be written on allow, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_GroupNonMemberDenied is the regression guard for the
// PR #361 blocking finding: any logged-in user could search ANY group's full
// message history by sending an arbitrary group_no — the middleware chain
// (Auth/Space/ratelimit/audit) never verified group membership.
func TestCheckChannelAccess_GroupNonMemberDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{}} // not a member
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "victim-group", "attacker") {
		t.Fatalf("non-member must be denied")
	}
	if gSvc.calls != 1 {
		t.Fatalf("ExistMemberActive should be consulted exactly once, got %d", gSvc.calls)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write an error response")
	}
	// Anti-enumeration: deny as NOT_FOUND, never a "forbidden, group exists" hint.
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_LookupErrorFailsClosed — a membership lookup error
// must deny (fail closed), not fall through to the OS query.
func TestCheckChannelAccess_LookupErrorFailsClosed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{err: errors.New("db down")}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("lookup error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write an error response")
	}
}

// TestCheckChannelAccess_ThreadUsesParentGroup — thread search authorizes
// against the parent group parsed from `{group_no}____{short_id}`.
func TestCheckChannelAccess_ThreadUsesParentGroup(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"G9": true}}
	h := newAuthzHandler(gSvc)
	c, _ := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "me") {
		t.Fatalf("parent-group member must pass the gate for thread search")
	}
	if gSvc.gotGroupNo != "G9" {
		t.Fatalf("thread gate must check the parent group, got %q", gSvc.gotGroupNo)
	}
}

func TestCheckChannelAccess_ThreadNonMemberDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{}}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "attacker") {
		t.Fatalf("non-member of parent group must be denied thread search")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write an error response")
	}
}

// TestCheckChannelAccess_ThreadMalformedIDDenied — an unparseable thread
// channel_id must deny, not silently skip the membership check (the
// empty-groupNo fallback in groupNoFromChannel is for display joins only).
func TestCheckChannelAccess_ThreadMalformedIDDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"": true}}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "____orphan", "me") {
		t.Fatalf("malformed thread channel_id must be denied")
	}
	if gSvc.calls != 0 {
		t.Fatalf("malformed id must be rejected before any membership lookup")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write an error response")
	}
}
