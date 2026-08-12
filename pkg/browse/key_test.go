package browse

import "testing"

func TestParseObjectKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want Object
	}{
		{
			name: "plain jsonl",
			key:  "jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
			want: Object{
				Layer: "jsonl", Source: "mattermost", Team: "-", Conversation: "chan-alpha",
				Year: "2017", Month: "2017-08", Key: "jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
			},
		},
		{
			name: "compressed markdown",
			key:  "markdown/mattermost/direct-messages/zack/2016/2016-10.md.zst",
			want: Object{
				Layer: "markdown", Source: "mattermost", Team: "direct-messages", Conversation: "zack",
				Year: "2016", Month: "2016-10", Compressed: true,
				Key: "markdown/mattermost/direct-messages/zack/2016/2016-10.md.zst",
			},
		},
		{
			name: "encrypted jsonl",
			key:  "jsonl/mattermost/team-quartz/chan-beta/2018/2018-03.jsonl.age",
			want: Object{
				Layer: "jsonl", Source: "mattermost", Team: "team-quartz", Conversation: "chan-beta",
				Year: "2018", Month: "2018-03", Encrypted: true,
				Key: "jsonl/mattermost/team-quartz/chan-beta/2018/2018-03.jsonl.age",
			},
		},
		{
			name: "compressed and encrypted",
			key:  "jsonl/mattermost/team-quartz/chan-beta/2019/2019-01.jsonl.zst.age",
			want: Object{
				Layer: "jsonl", Source: "mattermost", Team: "team-quartz", Conversation: "chan-beta",
				Year: "2019", Month: "2019-01", Compressed: true, Encrypted: true,
				Key: "jsonl/mattermost/team-quartz/chan-beta/2019/2019-01.jsonl.zst.age",
			},
		},
		{
			name: "ai export thread (no conversation segment)",
			key:  "jsonl/claude/account-a/2024/2024-03-14_101500.jsonl",
			want: Object{
				Layer: "jsonl", Source: "claude", Team: "account-a", Conversation: "",
				Year: "2024", Month: "2024-03-14_101500",
				Key: "jsonl/claude/account-a/2024/2024-03-14_101500.jsonl",
			},
		},
		{
			name: "ai export thread markdown compressed and encrypted",
			key:  "markdown/chatgpt/default/2024/2024-01-12_164500.md.zst.age",
			want: Object{
				Layer: "markdown", Source: "chatgpt", Team: "default", Conversation: "",
				Year: "2024", Month: "2024-01-12_164500", Compressed: true, Encrypted: true,
				Key: "markdown/chatgpt/default/2024/2024-01-12_164500.md.zst.age",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseObjectKey(tt.key)
			if err != nil {
				t.Fatalf("ParseObjectKey(%q) returned error: %v", tt.key, err)
			}
			if *got != tt.want {
				t.Fatalf("ParseObjectKey(%q) = %+v, want %+v", tt.key, *got, tt.want)
			}
		})
	}
}

func TestParseObjectKeyInvalid(t *testing.T) {
	for _, key := range []string{
		"",
		"jsonl/mattermost/-/chan-alpha/2017",
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.txt",
		"unknown/mattermost/-/chan-alpha/2017/2017-08.jsonl",
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl.extra",
	} {
		if _, err := ParseObjectKey(key); err == nil {
			t.Errorf("ParseObjectKey(%q) expected error, got none", key)
		}
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		layer, filename string
		month           string
		comp, enc       bool
	}{
		{"jsonl", "2017-08.jsonl", "2017-08", false, false},
		{"jsonl", "2017-08.jsonl.zst", "2017-08", true, false},
		{"jsonl", "2017-08.jsonl.age", "2017-08", false, true},
		{"jsonl", "2017-08.jsonl.zst.age", "2017-08", true, true},
		{"markdown", "2016-10.md.zst.age", "2016-10", true, true},
	}
	for _, tt := range tests {
		month, comp, enc, err := parseFilename(tt.layer, tt.filename)
		if err != nil {
			t.Fatalf("parseFilename(%q, %q) error: %v", tt.layer, tt.filename, err)
		}
		if month != tt.month || comp != tt.comp || enc != tt.enc {
			t.Errorf("parseFilename(%q, %q) = (%q,%v,%v), want (%q,%v,%v)",
				tt.layer, tt.filename, month, comp, enc, tt.month, tt.comp, tt.enc)
		}
	}
}
