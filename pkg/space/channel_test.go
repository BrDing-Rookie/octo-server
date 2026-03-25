package space

import "testing"

func TestBuildChannelID(t *testing.T) {
	tests := []struct {
		spaceID, peerID, want string
	}{
		{"", "user123", "user123"},
		{"sp1", "user123", "ssp1_user123"},
		{"42", "bot_abc", "s42_bot_abc"},
		{"minglue_default", "botfather", "sminglue_default_botfather"},
	}
	for _, tt := range tests {
		got := BuildChannelID(tt.spaceID, tt.peerID)
		if got != tt.want {
			t.Errorf("BuildChannelID(%q, %q) = %q, want %q", tt.spaceID, tt.peerID, got, tt.want)
		}
	}
}

func TestParseChannelID(t *testing.T) {
	RegisterSpaceIDs([]string{"sp1", "42", "minglue_default", "myspace"})

	tests := []struct {
		channelID, wantSpace, wantPeer string
	}{
		// bare UIDs not starting with 's'
		{"user123", "", "user123"},
		{"alice", "", "alice"},
		{"bob_bot", "", "bob_bot"},
		{"notspace", "", "notspace"},

		// bare UIDs starting with 's' — must NOT be mistaken for Space prefix
		{"steve_bot", "", "steve_bot"},
		{"support", "", "support"},
		{"sam_admin", "", "sam_admin"},
		{"submarine", "", "submarine"},

		// known spaceId matches
		{"ssp1_user123", "sp1", "user123"},
		{"s42_bot_abc", "42", "bot_abc"},

		// known spaceId with underscores in spaceId
		{"sminglue_default_botfather", "minglue_default", "botfather"},
		{"sminglue_default_test_1_bot", "minglue_default", "test_1_bot"},
		{"sminglue_default_xuhao", "minglue_default", "xuhao"},

		// empty string
		{"", "", ""},
	}
	for _, tt := range tests {
		gotSpace, gotPeer := ParseChannelID(tt.channelID)
		if gotSpace != tt.wantSpace || gotPeer != tt.wantPeer {
			t.Errorf("ParseChannelID(%q) = (%q, %q), want (%q, %q)",
				tt.channelID, gotSpace, gotPeer, tt.wantSpace, tt.wantPeer)
		}
	}
}

func TestParseChannelID_RegexFallback(t *testing.T) {
	// Clear known list so only regex fallback is used
	RegisterSpaceIDs(nil)

	hex32 := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6" // 32 hex chars
	tests := []struct {
		name                         string
		channelID, wantSpace, wantPeer string
	}{
		// valid: s + 32 hex + _ + peerID
		{"valid_hex32", "s" + hex32 + "_user123", hex32, "user123"},
		{"valid_hex32_underscore_peer", "s" + hex32 + "_bot_abc", hex32, "bot_abc"},

		// 31 hex chars — too short, not a space prefix
		{"short_31hex", "s" + hex32[:31] + "_user", "", "s" + hex32[:31] + "_user"},
		// 33 hex chars — too long, regex won't match at position 33
		{"long_33hex", "s" + hex32 + "f_user", "", "s" + hex32 + "f_user"},

		// non-hex character in the 32-char segment
		{"non_hex_char", "s" + "g1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6" + "_user", "", "s" + "g1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6" + "_user"},

		// missing underscore after 32 hex
		{"no_underscore", "s" + hex32 + "user", "", "s" + hex32 + "user"},

		// bare UID starting with 's' — must not match
		{"bare_s_uid", "steve_bot", "", "steve_bot"},
		{"bare_support", "support", "", "support"},

		// empty
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSpace, gotPeer := ParseChannelID(tt.channelID)
			if gotSpace != tt.wantSpace || gotPeer != tt.wantPeer {
				t.Errorf("ParseChannelID(%q) = (%q, %q), want (%q, %q)",
					tt.channelID, gotSpace, gotPeer, tt.wantSpace, tt.wantPeer)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	RegisterSpaceIDs([]string{"myspace", "minglue_default"})

	cases := []struct{ spaceID, peerID string }{
		{"myspace", "user456"},
		{"minglue_default", "botfather"},
		{"minglue_default", "test_1_bot"},
	}
	for _, tt := range cases {
		channelID := BuildChannelID(tt.spaceID, tt.peerID)
		gotSpace, gotPeer := ParseChannelID(channelID)
		if gotSpace != tt.spaceID || gotPeer != tt.peerID {
			t.Errorf("roundtrip(%q, %q) failed: channelID=%q, got (%q, %q)",
				tt.spaceID, tt.peerID, channelID, gotSpace, gotPeer)
		}
	}
}

func TestRoundTrip_UUID(t *testing.T) {
	// Simulate real UUID-based space IDs (no known list, regex fallback)
	RegisterSpaceIDs(nil)

	hex32 := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"
	cases := []struct{ spaceID, peerID string }{
		{hex32, "user456"},
		{hex32, "bot_abc_def"},
	}
	for _, tt := range cases {
		channelID := BuildChannelID(tt.spaceID, tt.peerID)
		gotSpace, gotPeer := ParseChannelID(channelID)
		if gotSpace != tt.spaceID || gotPeer != tt.peerID {
			t.Errorf("roundtrip(%q, %q) failed: channelID=%q, got (%q, %q)",
				tt.spaceID, tt.peerID, channelID, gotSpace, gotPeer)
		}
	}
}
