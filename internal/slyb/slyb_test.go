package slyb

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/nimling/nimbody/cli"
)

func TestNormalURL(t *testing.T) {
	cases := map[string]string{
		"https://GitHub.com/SlyOtis/lcp/?tab=1#readme": "https://github.com/SlyOtis/lcp",
		"https://example.com/openapi.json":             "",
		"https://git.example.com/api/v3/$1":            "",
		"http://localhost:4600/page/overview.":         "http://localhost:4600/page/overview",
		"http://schemas.datacontract.org/2004/07":      "",
	}
	for raw, want := range cases {
		if got := normalURL(raw); got != want {
			t.Errorf("normalURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPutPrefsReplacesBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.js")
	if err := os.WriteFile(path, []byte("user_pref(\"a\", 1);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := putPrefs(path); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if strings.Count(text, prefsStart) != 1 || !strings.HasPrefix(text, "user_pref(\"a\", 1);\n") {
		t.Fatalf("user.js not kept with one block:\n%s", text)
	}
	if !strings.Contains(text, `user_pref("identity.fxaccounts.enabled", false);`) {
		t.Fatal("account sign in is not turned off")
	}
}

func TestGetDiaSpace(t *testing.T) {
	dir := t.TempDir()
	bookmarks := `{"roots":{"bookmark_bar":{"children":[{"type":"folder","name":"Personal","children":[
		{"type":"url","name":"Photos","url":"https://photos.google.com/"},
		{"type":"folder","name":"AI","children":[{"type":"url","name":"Claude","url":"https://claude.ai/"},
			{"type":"folder","name":"LLMs","children":[{"type":"url","name":"Ollama","url":"https://ollama.com/"}]}]}]}]},
		"other":{"children":[{"type":"url","name":"News","url":"https://nrk.no/"}]},"synced":{"children":[]}}}`
	if err := os.WriteFile(filepath.Join(dir, "Bookmarks"), []byte(bookmarks), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(dir, "Sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("SNSS"), 3, 0, 0, 0)
	data = append(data, snssCommand(snssUpdateTabNavigation, snssNavigation(1, 0, "https://zen.dev", "Zen"))...)
	if err := os.WriteFile(filepath.Join(sessions, "Session_1"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	space, err := GetDiaSpace(DiaProfile{Dir: dir, Name: "Personal"}, "Personal")
	if err != nil {
		t.Fatal(err)
	}
	if len(space.Tabs) != 1 || space.Tabs[0].Title != "Photos" {
		t.Fatalf("pinned = %+v", space.Tabs)
	}
	labels := []string{}
	for _, folder := range space.Folders {
		labels = append(labels, folder.Label)
	}
	if strings.Join(labels, "|") != "AI|AI / LLMs|Other bookmarks" {
		t.Fatalf("folders = %v", labels)
	}
	if len(space.Loose) != 1 || space.Loose[0].URL != "https://zen.dev" {
		t.Fatalf("open tabs = %+v", space.Loose)
	}
}

func snssCommand(id byte, payload []byte) []byte {
	size := len(payload) + 1
	return append([]byte{byte(size), byte(size >> 8), id}, payload...)
}

func snssNavigation(tab int32, index int32, url, title string) []byte {
	body := binary.LittleEndian.AppendUint32(nil, uint32(tab))
	body = binary.LittleEndian.AppendUint32(body, uint32(index))
	body = binary.LittleEndian.AppendUint32(body, uint32(len(url)))
	body = append(body, url...)
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	units := utf16.Encode([]rune(title))
	body = binary.LittleEndian.AppendUint32(body, uint32(len(units)))
	for _, unit := range units {
		body = binary.LittleEndian.AppendUint16(body, unit)
	}
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(body))), body...)
}

func TestGetSessionTabs(t *testing.T) {
	pair := func(a, b int32) []byte {
		return binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, uint32(a)), uint32(b))
	}
	data := append([]byte("SNSS"), 3, 0, 0, 0)
	data = append(data, snssCommand(snssUpdateTabNavigation, snssNavigation(1, 0, "https://old.dev", "Old"))...)
	data = append(data, snssCommand(snssUpdateTabNavigation, snssNavigation(1, 1, "https://zen.dev", "Zen æøå"))...)
	data = append(data, snssCommand(snssSetSelectedNavIndex, pair(1, 1))...)
	data = append(data, snssCommand(snssSetPinnedState, append(binary.LittleEndian.AppendUint32(nil, 1), 1, 0, 0, 0))...)
	data = append(data, snssCommand(snssUpdateTabNavigation, snssNavigation(2, 0, "https://closed.dev", "Closed"))...)
	data = append(data, snssCommand(snssTabClosed, append(binary.LittleEndian.AppendUint32(nil, 2), make([]byte, 8)...))...)
	path := filepath.Join(t.TempDir(), "Session_1")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	tabs, err := GetSessionTabs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 1 || tabs[0].URL != "https://zen.dev" || tabs[0].Title != "Zen æøå" || !tabs[0].Pinned {
		t.Fatalf("tabs = %+v", tabs)
	}
}

func TestGetArcSpaces(t *testing.T) {
	sidebar := `{"sidebar":{"containers":[{"global":{}},{
		"topAppsContainerIDs":[{"default":true},"top"],"spaces":["s1",{"id":"s1","title":"Work","containerIDs":["unpinned","u1","pinned","p1"],"customInfo":{"iconType":{"emoji_v2":"💼"}}}],
		"items":["top",{"id":"top","childrenIds":["m1"],"data":{"itemContainer":{}}},"m1",{"id":"m1","title":null,"childrenIds":[],"data":{"tab":{"savedURL":"https://mail.google.com","savedTitle":"Mail"}}},"u1",{"id":"u1","childrenIds":["h1"],"data":{"itemContainer":{}}},"h1",{"id":"h1","title":null,"childrenIds":[],"data":{"tab":{"savedURL":"https://news.ycombinator.com","savedTitle":"HN"}}},"p1",{"id":"p1","childrenIds":["t1","f1"],"data":{"itemContainer":{}}},
			"t1",{"id":"t1","title":null,"childrenIds":[],"data":{"tab":{"savedURL":"https://github.com","savedTitle":"GitHub"}}},
			"f1",{"id":"f1","title":"Docs","childrenIds":["t2"],"data":{"list":{}}},
			"t2",{"id":"t2","title":"Go","childrenIds":[],"data":{"tab":{"savedURL":"https://go.dev","savedTitle":"The Go Language"}}}]}]}}`
	path := filepath.Join(t.TempDir(), "StorableSidebar.json")
	if err := os.WriteFile(path, []byte(sidebar), 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := GetArcSpaces(path)
	if err != nil {
		t.Fatal(err)
	}
	spaces := imported.Spaces
	if len(imported.Essentials) != 1 || imported.Essentials[0].URL != "https://mail.google.com" {
		t.Fatalf("essentials = %+v", imported.Essentials)
	}
	if len(spaces[0].Loose) != 1 || spaces[0].Loose[0].URL != "https://news.ycombinator.com" {
		t.Fatalf("loose = %+v", spaces[0].Loose)
	}
	if len(spaces) != 1 || spaces[0].Name != "Work" || spaces[0].Icon != "💼" {
		t.Fatalf("spaces = %+v", spaces)
	}
	work := spaces[0]
	if len(work.Tabs) != 1 || work.Tabs[0].Title != "GitHub" || len(work.Folders) != 1 || work.Folders[0].Label != "Docs" || work.Folders[0].Tabs[0].Title != "Go" {
		t.Fatalf("work = %+v", work)
	}
}

func TestMatchRules(t *testing.T) {
	store := RuleStore{Rules: []Rule{
		{Name: "acme space", Scope: ScopeSpace, Space: "acme-space", Script: `text := import("text"); result = text.has_suffix(host, "example.com")`},
		{ID: "r2", Name: "acme", Scope: ScopeGroup, Space: "acme-space", Group: "platform", Script: `text := import("text"); result = text.has_suffix(host, "git.example.com")`},
		{Name: "docs", Scope: ScopeSection, Space: "acme-space", Group: "platform", Section: "Docs", Script: `text := import("text"); result = text.contains(path, "/docs")`},
	}}
	match, err := MatchRules(t.Context(), store, Page{URL: "https://git.example.com/platform/hooks"})
	if err != nil {
		t.Fatal(err)
	}
	if match.Space != "acme-space" || match.Group != "platform" || match.Section != "" || match.Rule != "acme" || match.ID != "r2" {
		t.Fatalf("match = %+v", match)
	}
	none, err := MatchRules(t.Context(), store, Page{URL: "https://example.org/"})
	if err != nil || none.Space != "" {
		t.Fatalf("none = %+v, %v", none, err)
	}
	if _, err := MatchRules(t.Context(), RuleStore{Rules: []Rule{{Name: "broken", Scope: ScopeSpace, Script: "result = "}}}, Page{URL: "https://a.b/"}); err == nil {
		t.Fatal("a broken rule must fail")
	}
}

func TestJourneySince(t *testing.T) {
	from, err := journeySince(Profile{JourneyStart: "06:30"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if from.Hour() != 6 || from.Minute() != 30 || from.After(time.Now()) {
		t.Fatalf("day start = %v", from)
	}
	picked, err := journeySince(Profile{}, "2026-10-26T09:00")
	if err != nil || picked.Day() != 26 || picked.Hour() != 9 {
		t.Fatalf("picked = %v, %v", picked, err)
	}
	if _, err := journeySince(Profile{}, "yesterday"); err == nil {
		t.Fatal("an unreadable time must be refused")
	}
}

func TestProjectSpace(t *testing.T) {
	ties := SpaceTies{Spaces: map[string]SpaceTie{
		"work":  {Folders: []string{"/home/u/Projects"}},
		"acme":  {Folders: []string{"/home/u/Projects/acme"}},
		"other": {Folders: []string{"/home/u/Projects-old"}},
	}}
	rules := RuleStore{Rules: []Rule{{Name: "acme email", Scope: ScopeSpace, Space: "acme", Script: `text := import("text"); result = text.has_suffix(email, "@example.com")`}}}
	cases := map[string]Project{
		"acme":  {Path: "/home/u/Projects/acme/platform"},
		"work":  {Path: "/home/u/Projects/work"},
		"other": {Path: "/home/u/Projects-old/x"},
	}
	for want, project := range cases {
		if got := projectSpace(t.Context(), project, ties, rules, GroupStore{}); got != want {
			t.Fatalf("%s went to %q, want %q", project.Path, got, want)
		}
	}
	if got := projectSpace(t.Context(), Project{Path: "/elsewhere/repo", Email: "me@example.com"}, ties, rules, GroupStore{}); got != "acme" {
		t.Fatalf("email rule placed it in %q", got)
	}
}

func TestGetSiteFacts(t *testing.T) {
	history := []Visit{{URL: "https://www.youtube.com/", Title: "YouTube", Visits: 2, LastVisit: "2026-10-27T23:14:15Z"}}
	spans := []FocusSpan{
		{Start: "2026-10-27T20:00:00Z", End: "2026-10-27T20:40:00Z", URL: "https://www.youtube.com/watch?v=a", Title: "Godot 4 in an hour"},
		{Start: "2026-10-27T21:00:00Z", End: "2026-10-27T21:10:00Z", URL: "http://localhost:5173/", Title: "Components", DevTools: true},
	}
	sites := GetSiteFacts(history, spans)
	tube := sites["youtube.com"]
	if tube == nil || tube.Visits != 2 || tube.Pages != 2 || tube.Minutes != 40 || tube.Top[0] != "Godot 4 in an hour" || tube.First != "2026-10-27T20:00:00Z" {
		t.Fatalf("youtube = %+v", tube)
	}
	if local := sites["localhost"]; local == nil || local.DevTools != 10 || local.Minutes != 10 {
		t.Fatalf("localhost = %+v", local)
	}
}

func TestGitCommits(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.txt")
	git("commit", "-q", "-m", "add a")
	commits := gitCommits(t.Context(), root, "t@example.com", "https://github.com/o/r", time.Now().Add(-time.Hour))
	if len(commits) != 1 || commits[0].Subject != "add a" || commits[0].Added != 2 || commits[0].Files != 1 || !strings.HasPrefix(commits[0].URL, "https://github.com/o/r/commit/") {
		t.Fatalf("commits = %+v", commits)
	}
	if other := gitCommits(t.Context(), root, "someone@else.no", "", time.Now().Add(-time.Hour)); len(other) != 0 {
		t.Fatalf("another author's commits = %+v", other)
	}
}

func TestRelatesTo(t *testing.T) {
	cases := map[string]bool{"example.com": true, "login.example.com": true, "git.example.com": true, "python.org": false, "olavexample.com": false, "no": false}
	for host, want := range cases {
		if got := relatesTo(host, "example.com"); got != want {
			t.Fatalf("relatesTo(%q) = %v, want %v", host, got, want)
		}
	}
	if normalDomain("https://www.Example.com/about") != "example.com" || normalDomain("example.com") != "example.com" {
		t.Fatal("domains are normalized to their host")
	}
}

func TestDiscardPages(t *testing.T) {
	pages := []sortCandidate{
		{ID: "1", Title: "Pull request #36", URL: "https://github.com/o/r/pull/36", Tab: true},
		{ID: "2", Title: "Pull requests", URL: "https://github.com/o/r/pulls", Tab: true},
		{ID: "3", Title: "o/r", URL: "https://github.com/o/r", Tab: true},
		{ID: "4", Title: "Pull request #36", URL: "https://www.github.com/o/r/pull/36#top", Tab: true},
		{ID: "5", Title: "Sign in to your account", URL: "https://login.microsoftonline.com/common/oauth2/authorize", Tab: true},
		{ID: "6", Title: "Portal", URL: "https://login.microsoftonline.com/portal/home", Tab: true},
		{ID: "7", Title: "Page not found", URL: "https://example.org/x", Tab: true},
		{ID: "8", Title: "Slack", URL: "https://app.slack.com/client/T1", Tab: true},
	}
	kept, discarded := discardPages(pages)
	ids := []string{}
	for _, page := range kept {
		ids = append(ids, page.ID)
	}
	if strings.Join(ids, ",") != "1,2,6,8" {
		t.Fatalf("kept %v, discarded %+v", ids, discarded)
	}
	reasons := map[string]string{}
	for _, entry := range discarded {
		reasons[entry.Tab] = entry.Reason
	}
	if reasons["4"] != "duplicate" || reasons["7"] != "error page" || !strings.HasPrefix(reasons["3"], "a step") || reasons["5"] != "a sign-in that led on" {
		t.Fatalf("reasons = %v", reasons)
	}
}

func TestBuildPrefixes(t *testing.T) {
	if got := catalogPrefix("https://github.com/acme/widgets/pull/3"); got != "github.com/acme/widgets" {
		t.Fatalf("code host prefix = %q", got)
	}
	if got := catalogPrefix("https://www.app.tst.example.com/rooms"); got != "app.tst.example.com" {
		t.Fatalf("site prefix = %q", got)
	}
	cases := map[string]bool{
		"https://github.com/acme/widgets":             true,
		"https://github.com/acme/widgets/pull/3":      true,
		"https://github.com/acme/widgets-extra":       false,
		"https://auth.tst.example.com/login":          true,
		"https://example.org/github.com/acme/widgets": false,
	}
	for address, want := range cases {
		prefix := "github.com/acme/widgets"
		if strings.Contains(address, "example.com") {
			prefix = "tst.example.com"
		}
		if got := underPrefix(address, prefix); got != want {
			t.Fatalf("underPrefix(%q, %q) = %v", address, prefix, got)
		}
	}
}

func TestGroupUpdateSpace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := GroupStore{
		Groups: []Group{{ID: "g1", Title: "one", Space: "w", SpaceID: "s1"}},
		Spaces: []Space{{ID: "s1", Name: "Space", Workspace: "w"}, {ID: "s2", Name: "Other", Workspace: "x"}},
	}
	if err := PutGroups(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		command := NewGroupCommand()
		command.SetArgs(append([]string{"update"}, args...))
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		return command.Execute()
	}
	if err := run("g1", "--space", "Library"); err != nil {
		t.Fatal(err)
	}
	got, err := GetGroups()
	if err != nil {
		t.Fatal(err)
	}
	library := got.SpaceNamed("w", "Library")
	if library == nil || got.Groups[0].SpaceID != library.ID {
		t.Fatalf("group not moved into a new Library space: %+v", got)
	}
	if err := run("g1", "--space", "s2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetGroups(); got.Groups[0].SpaceID == "s2" {
		t.Fatal("moved into a space of another workspace")
	}
	if err := run("g1", "--space", "s1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := GetGroups(); got.Groups[0].SpaceID != "s1" {
		t.Fatalf("not moved back by id: %+v", got.Groups[0])
	}
}

func TestRuleUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := PutRules(RuleStore{Rules: []Rule{{ID: "r1", Name: "old", Scope: ScopeSpace, Space: "s", Script: "result = false"}}}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		command := NewRuleCommand()
		command.SetArgs(append([]string{"update"}, args...))
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		return command.Execute()
	}
	if err := run("r1", "--name", "new", "--script", `result = host == "a.b"`); err != nil {
		t.Fatal(err)
	}
	store, err := GetRules()
	if err != nil || store.Rules[0].Name != "new" || store.Rules[0].Script != `result = host == "a.b"` {
		t.Fatalf("rules = %+v, %v", store, err)
	}
	if err := run("r1"); err == nil {
		t.Fatal("an update with nothing to change must fail")
	}
	if err := run("missing", "--name", "x"); err == nil {
		t.Fatal("an unknown rule must fail")
	}
	if err := run("r1", "--script", "result = "); err == nil {
		t.Fatal("a broken script must fail")
	}
}

func TestLiveCommands(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	run := func(args ...string) (string, error) {
		var out strings.Builder
		command := NewLiveCommand()
		command.SetArgs(append(args, "--json"))
		AddFlags(command)
		command.SetOut(&out)
		command.SetErr(io.Discard)
		err := command.Execute()
		return out.String(), err
	}
	page := "https://mail.example.test/inbox"
	if _, err := run("create", page, "--every", "15"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("create", page); cli.ExitCode(err) != cli.ExitRejected {
		t.Fatalf("a second create must be rejected, got %v", err)
	}
	if _, err := run("create", "about:blank"); cli.ExitCode(err) != cli.ExitRejected {
		t.Fatalf("a page that is not http must be rejected, got %v", err)
	}
	store, err := GetLive()
	if err != nil || len(store.Items) != 1 || store.Items[0].Source != LiveTitle || store.Items[0].Every != 15 || store.Items[0].SyncedAt == "" {
		t.Fatalf("live = %+v, %v", store, err)
	}
	if _, err := run("update", page); cli.ExitCode(err) != cli.ExitRejected {
		t.Fatalf("an update with nothing to change must be rejected, got %v", err)
	}
	if _, err := run("update", page, "--every", "0"); err != nil {
		t.Fatal(err)
	}
	out, err := run("get", page)
	if err != nil || !strings.Contains(out, `"every": 0`) || !strings.Contains(out, `"source": "title"`) {
		t.Fatalf("get = %s, %v", out, err)
	}
	if _, err := run("delete", "https://other.example.test"); cli.ExitCode(err) != cli.ExitRejected {
		t.Fatalf("deleting an unknown item must be rejected, got %v", err)
	}
	if _, err := run("delete", page); err != nil {
		t.Fatal(err)
	}
	if store, _ := GetLive(); len(store.Items) != 0 {
		t.Fatalf("not deleted: %+v", store)
	}
	if getLiveSource("https://github.com/SlyOtis/quillborne") != LiveGitHub || getLiveSource("https://github.com/SlyOtis/quillborne/pulls") != LiveTitle {
		t.Fatal("only a repository page reads from GitHub")
	}
}

func TestRemoteCommands(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "slyb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".slyb"), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(home, ".slyb", "browser.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	methods := make(chan string, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var call request
			_ = json.NewDecoder(conn).Decode(&call)
			methods <- call.Method
			_, _ = conn.Write([]byte(`{"result":{"available":true,"host":"127.0.0.1","port":9222,"session":false,"started":"runtime"}}`))
			conn.Close()
		}
	}()
	command := NewRemoteCommand()
	for name, want := range map[string]string{"get": "read", "create": "write", "delete": "destructive"} {
		leaf, _, err := command.Find([]string{name})
		if err != nil || cli.Access(leaf) != want {
			t.Fatalf("remote %s access = %q, want %q (%v)", name, cli.Access(leaf), want, err)
		}
	}
	for name, method := range map[string]string{"get": "remote.get", "create": "remote.create", "delete": "remote.delete"} {
		var out strings.Builder
		command := NewRemoteCommand()
		command.SetArgs([]string{name, "--json"})
		AddFlags(command)
		command.SetOut(&out)
		command.SetErr(io.Discard)
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if got := <-methods; got != method {
			t.Fatalf("remote %s called %s, want %s", name, got, method)
		}
		if !strings.Contains(out.String(), `"port": 9222`) || !strings.Contains(out.String(), `"host": "127.0.0.1"`) {
			t.Fatalf("remote %s printed %s", name, out.String())
		}
	}
	var out strings.Builder
	command = NewRemoteCommand()
	command.SetArgs([]string{"create", "extra"})
	command.SetOut(&out)
	command.SetErr(io.Discard)
	if cli.ExitCode(command.Execute()) == cli.ExitOK {
		t.Fatal("create takes no argument")
	}
}
