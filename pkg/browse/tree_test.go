package browse

import "testing"

func TestBuildTree(t *testing.T) {
	keys := []string{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
		"jsonl/mattermost/-/chan-alpha/2017/2017-09.jsonl",
		"jsonl/mattermost/-/chan-alpha/2018/2018-05.jsonl",
		"markdown/mattermost/-/chan-alpha/2017/2017-08.md",
		"jsonl/mattermost/direct-messages/zack/2016/2016-10.jsonl",
		"jsonl/mattermost/team-quartz/chan-beta/2019/2019-01.jsonl.zst.age",
		"markdown/mattermost/team-quartz/chan-beta/2019/2019-01.md.zst.age",
	}

	tree, err := BuildTree(keys)
	if err != nil {
		t.Fatal(err)
	}

	root := tree.Root
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want 2 (jsonl, markdown)", len(root.Children))
	}
	if root.Children[0].Slug != "jsonl" || root.Children[1].Slug != "markdown" {
		t.Fatalf("layer order = %q, %q; want jsonl, markdown", root.Children[0].Slug, root.Children[1].Slug)
	}

	// Cosmetic labels for pseudo-teams.
	jsonl := root.Children[0]
	mattermost := jsonl.Children[0]
	if mattermost.Slug != "mattermost" {
		t.Fatalf("source = %q, want mattermost", mattermost.Slug)
	}
	if len(mattermost.Children) != 3 {
		t.Fatalf("teams = %d, want 3", len(mattermost.Children))
	}
	teams := mattermost.Children
	if teams[0].Slug != "direct-messages" || teams[0].Label != "Direct messages" {
		t.Errorf("team[0] = %q/%q, want direct-messages/Direct messages (alphabetical)", teams[0].Slug, teams[0].Label)
	}
	if teams[1].Slug != "-" || teams[1].Label != "No team" {
		t.Errorf("team[1] slug = %q/%q, want -/No team", teams[1].Slug, teams[1].Label)
	}
	if teams[2].Slug != "team-quartz" {
		t.Errorf("team[2] slug = %q, want team-quartz", teams[2].Slug)
	}

	// Years descending, months ascending.
	general := teams[1].Children[0]
	if general.Slug != "chan-alpha" {
		t.Fatalf("conversation = %q, want chan-alpha", general.Slug)
	}
	if len(general.Children) != 2 {
		t.Fatalf("years = %d, want 2", len(general.Children))
	}
	if general.Children[0].Slug != "2018" || general.Children[1].Slug != "2017" {
		t.Errorf("year order = %q, %q; want 2018, 2017 (descending)", general.Children[0].Slug, general.Children[1].Slug)
	}
	months := general.Children[1].Children
	if len(months) != 2 || months[0].Slug != "2017-08" || months[1].Slug != "2017-09" {
		t.Errorf("month order = %v, want 2017-08, 2017-09 (ascending)", months)
	}

	// Suffix variants of the same month deduplicate into one leaf.
	random := teams[2].Children[0]
	if random.Slug != "chan-beta" {
		t.Fatalf("conversation = %q, want chan-beta", random.Slug)
	}
	year := random.Children[0]
	if year.Slug != "2019" {
		t.Fatalf("year = %q, want 2019", year.Slug)
	}
	if len(year.Children) != 1 {
		t.Fatalf("months = %d, want 1 (deduplicated)", len(year.Children))
	}
	if year.Children[0].Object.Compressed != true || year.Children[0].Object.Encrypted != true {
		t.Errorf("month object = %+v, want compressed+encrypted", year.Children[0].Object)
	}
}

func TestBuildTreeThreadLayout(t *testing.T) {
	keys := []string{
		"jsonl/claude/account-a/2024/2024-03-14_101500.jsonl",
		"jsonl/claude/account-a/2024/2024-03-14_093200.jsonl.zst",
		"markdown/claude/account-a/2024/2024-03-14_101500.md.zst",
	}

	tree, err := BuildTree(keys)
	if err != nil {
		t.Fatal(err)
	}

	jsonl := tree.Root.Children[0]
	claude := jsonl.Children[0]
	if claude.Slug != "claude" {
		t.Fatalf("source = %q, want claude", claude.Slug)
	}
	account := claude.Children[0]
	if account.Slug != "account-a" {
		t.Fatalf("team = %q, want account-a", account.Slug)
	}
	year := account.Children[0]
	if year.Slug != "2024" {
		t.Fatalf("year = %q, want 2024", year.Slug)
	}
	if len(year.Children) != 2 {
		t.Fatalf("threads = %d, want 2 (deduplicated leaves)", len(year.Children))
	}
	if year.Children[0].Slug != "2024-03-14_093200" {
		t.Errorf("thread leaf[0] = %q, want 2024-03-14_093200", year.Children[0].Slug)
	}
	if year.Children[0].Object.Compressed != true {
		t.Errorf("thread leaf[0] compressed = false, want true")
	}
	if year.Children[1].Slug != "2024-03-14_101500" {
		t.Errorf("thread leaf[1] = %q, want 2024-03-14_101500", year.Children[1].Slug)
	}
}

func TestBuildTreeIgnoresNonObjectKeys(t *testing.T) {
	keys := []string{
		"jsonl/mattermost/-/chan-alpha/2017/2017-08.jsonl",
		"some/other/key",
		"jsonl/mattermost/",
	}
	tree, err := BuildTree(keys)
	if err != nil {
		t.Fatal(err)
	}
	jsonl := tree.Root.Children[0]
	if len(jsonl.Children) != 1 {
		t.Fatalf("sources = %d, want 1", len(jsonl.Children))
	}
	if len(tree.Root.Children) != 1 {
		t.Fatalf("layers = %d, want 1 (only jsonl)", len(tree.Root.Children))
	}
}
