package featuretasks

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/githubapi"
	"github.com/svrforum/SFPanel/internal/tasks"
)

var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
)

const (
	repoURL   = "https://github.com/acme/ops"
	previewAt = "/api/v1/cluster/tasks/import/preview"
	importAt  = "/api/v1/cluster/tasks/import"
)

// fakeGitHub is GitHub's REST API over one repository, acme/ops, whose
// branches point at commits and whose commits are each a set of files. It
// records every call.
type fakeGitHub struct {
	mu       sync.Mutex
	branches map[string]string            // branch → commit
	trees    map[string]map[string]string // commit → path → content
	// submodules are paths a listing shows as GitHub shows a submodule: a
	// "file" with no download_url.
	submodules map[string]bool
	gone       bool // the repository is missing
	// before runs ahead of every answer; true means it answered.
	before func(w http.ResponseWriter, r *http.Request) bool
	calls  []string // "commits <ref>", "list <dir> <ref>", "file <path> <ref>", "repo"
	auth   []string // each call's Authorization
}

func newGitAPI(t *testing.T) (*apiFixture, *fakeGitHub) {
	t.Helper()
	gh := &fakeGitHub{branches: map[string]string{}, trees: map[string]map[string]string{}, submodules: map[string]bool{}}
	srv := httptest.NewServer(gh)
	prev := githubapi.BaseURL
	githubapi.BaseURL = srv.URL
	t.Cleanup(func() { githubapi.BaseURL = prev; srv.Close() })
	return newAPI(t), gh
}

// commit records files as commit sha, which branch now points at.
func (g *fakeGitHub) commit(branch, sha string, files map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trees[sha] = files
	g.branches[branch] = sha
}

func (g *fakeGitHub) callLog() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/repos/"), "/", 3)
	rest := ""
	if len(parts) == 3 {
		rest = parts[2]
	}
	ref := r.URL.Query().Get("ref")
	call := "repo"
	switch {
	case strings.HasPrefix(rest, "commits/"):
		call = "commits " + strings.TrimPrefix(rest, "commits/")
	case rest == "contents" || strings.HasPrefix(rest, "contents/"):
		p := strings.TrimPrefix(strings.TrimPrefix(rest, "contents"), "/")
		if r.Header.Get("Accept") == "application/vnd.github.raw" {
			call = "file " + p + " " + ref
		} else {
			call = "list " + p + " " + ref
		}
	}
	g.mu.Lock()
	g.calls = append(g.calls, call)
	g.auth = append(g.auth, r.Header.Get("Authorization"))
	before, gone := g.before, g.gone
	g.mu.Unlock()
	if before != nil && before(w, r) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(parts) < 2 || !strings.EqualFold(parts[0], "acme") || !strings.EqualFold(parts[1], "ops") || gone {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	at := func(ref string) (map[string]string, string) {
		if ref == "" {
			ref = "main"
		}
		if sha, ok := g.branches[ref]; ok {
			return g.trees[sha], sha
		}
		if tree, ok := g.trees[ref]; ok {
			return tree, ref
		}
		return nil, ""
	}
	switch {
	case call == "repo":
		_, _ = w.Write([]byte(`{"full_name":"acme/ops"}`))
	case strings.HasPrefix(call, "commits "):
		_, sha := at(strings.TrimPrefix(rest, "commits/"))
		if sha == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write([]byte(sha))
	case strings.HasPrefix(call, "file "):
		tree, _ := at(ref)
		body, ok := tree[strings.TrimPrefix(strings.TrimPrefix(rest, "contents"), "/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	default:
		tree, _ := at(ref)
		dir := strings.TrimPrefix(strings.TrimPrefix(rest, "contents"), "/")
		g.list(w, tree, dir)
	}
}

type fakeItem struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	SHA         string  `json:"sha"`
	Size        int     `json:"size"`
	DownloadURL *string `json:"download_url"`
}

func (g *fakeGitHub) item(p, body string) fakeItem {
	dl := "https://raw.example/" + p
	it := fakeItem{Type: "file", Name: path.Base(p), Path: p, SHA: githubapi.BlobSHA([]byte(body)), Size: len(body), DownloadURL: &dl}
	if g.submodules[p] {
		it.DownloadURL = nil
	}
	return it
}

// list answers a folder one level deep, or the file dir names.
func (g *fakeGitHub) list(w http.ResponseWriter, tree map[string]string, dir string) {
	if body, ok := tree[dir]; ok {
		_ = json.NewEncoder(w).Encode(g.item(dir, body))
		return
	}
	var items []fakeItem
	seen := map[string]bool{}
	for p, body := range tree {
		rel := p
		if dir != "" {
			if !strings.HasPrefix(p, dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(p, dir+"/")
		}
		if first, _, deeper := strings.Cut(rel, "/"); deeper {
			if !seen[first] {
				seen[first] = true
				items = append(items, fakeItem{Type: "dir", Name: first, Path: path.Join(dir, first)})
			}
			continue
		}
		items = append(items, g.item(p, body))
	}
	if items == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	_ = json.NewEncoder(w).Encode(items)
}

// gitTask stores a task imported from repo at path on branch, whose document
// is doc.
func (f *apiFixture) gitTask(id, repo, branch, p, doc string) {
	f.t.Helper()
	task, problems := tasks.Parse([]byte(doc))
	if task == nil {
		f.t.Fatalf("the test's task does not parse: %+v", problems)
	}
	src := cluster.TaskSource{Kind: cluster.TaskSourceGit, Repo: repo, Branch: branch, Path: p, SHA: commitA}
	if err := f.env.store.PutTask(cluster.StoredTask{ID: id, Name: task.Name, YAML: doc, Source: src}); err != nil {
		f.t.Fatal(err)
	}
}

func named(name string) string {
	return "name: " + name + "\nsteps:\n  - command: { run: \"true\" }\n"
}

// previewAnswer and importAnswer are the two routes' data as the screen
// reads it.
type previewAnswer struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Files  []struct {
		Path     string `json:"path"`
		Name     string `json:"name"`
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		State    string `json:"state"`
		TaskID   string `json:"task_id"`
		TaskName string `json:"task_name"`
	} `json:"files"`
	Removed []struct {
		TaskID   string `json:"task_id"`
		TaskName string `json:"task_name"`
		Path     string `json:"path"`
	} `json:"removed"`
}

type importAnswer struct {
	Commit   string             `json:"commit"`
	Imported []taskView         `json:"imported"`
	Failed   []response.Warning `json:"failed"`
}

func TestThePreviewSortsAFolderAgainstTheTasksImportedFromIt(t *testing.T) {
	f, gh := newGitAPI(t)
	big := named("big") + "#" + strings.Repeat("x", tasks.MaxDocumentBytes)
	gh.commit("main", commitA, map[string]string{
		"tasks/web.yaml":    named("web"),
		"tasks/db.yml":      named("db backup") + "# changed\n",
		"tasks/ok.yaml":     named("ok"),
		"tasks/big.yaml":    big,
		"tasks/README.md":   "not a task",
		"tasks/sub/x.yaml":  named("deeper"),
		"tasks/vendor.yaml": "",
		"other/y.yaml":      named("elsewhere"),
	})
	gh.submodules["tasks/vendor.yaml"] = true
	// The stored repository is spelled in another case: GitHub's names are
	// not case-sensitive.
	f.gitTask("db1", "https://github.com/Acme/Ops", "main", "tasks/db.yml", named("db backup"))
	f.gitTask("ok1", repoURL, "main", "tasks/ok.yaml", named("ok"))
	f.gitTask("old1", repoURL, "main", "tasks/old.yaml", named("old"))
	// None of these is the folder's: another folder, branch, repository.
	f.gitTask("sub1", repoURL, "main", "tasks/sub/gone.yaml", named("sub gone"))
	f.gitTask("dev1", repoURL, "dev", "tasks/dev.yaml", named("dev gone"))
	f.gitTask("far1", "https://github.com/acme/other", "main", "tasks/far.yaml", named("far gone"))
	f.store("u1", named("mine"))

	a := f.do("POST", previewAt, map[string]string{"url": repoURL, "path": "tasks"})
	if a.Status != 200 {
		t.Fatalf("preview = %d %+v", a.Status, a.Error)
	}
	if strings.Contains(string(a.Data), `"yaml":`) || strings.Contains(string(a.Data), "steps") {
		t.Errorf("the preview carries documents: %s", a.Data)
	}
	var p previewAnswer
	a.data(t, &p)
	if p.Repo != repoURL || p.Branch != "main" || p.Path != "tasks" || p.Commit != commitA {
		t.Errorf("preview names %s %s %s %s, want the repo, main (left empty), tasks, the branch's commit", p.Repo, p.Branch, p.Path, p.Commit)
	}
	var got []string
	for _, fl := range p.Files {
		got = append(got, fmt.Sprintf("%s %s %s %s", fl.Path, fl.State, fl.TaskID, fl.TaskName))
		if fl.Name != path.Base(fl.Path) || fl.SHA == "" || fl.Size == 0 {
			t.Errorf("file %+v, want its name, blob SHA and size", fl)
		}
	}
	want := []string{
		"tasks/big.yaml too_large  ",
		"tasks/db.yml changed db1 db backup",
		"tasks/ok.yaml unchanged ok1 ok",
		"tasks/web.yaml new  ",
	}
	if !slices.Equal(got, want) {
		t.Errorf("files =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(p.Removed) != 1 || p.Removed[0].TaskID != "old1" || p.Removed[0].TaskName != "old" || p.Removed[0].Path != "tasks/old.yaml" {
		t.Errorf("removed = %+v, want old1 alone", p.Removed)
	}
	if calls := gh.callLog(); !slices.Equal(calls, []string{"commits main", "list tasks " + commitA}) {
		t.Errorf("GitHub calls = %q, want the branch resolved and the folder listed at its commit", calls)
	}
	if n := len(f.env.store.tasks); n != 7 {
		t.Errorf("stored tasks = %d, want the 7 there were: a preview writes nothing", n)
	}
}

// A refresh with nothing new asks GitHub twice, however many files there are.
func TestAPreviewWithNothingChangedAsksGitHubTwice(t *testing.T) {
	f, gh := newGitAPI(t)
	files := map[string]string{}
	for i := range 10 {
		p := fmt.Sprintf("t%02d.yaml", i)
		files[p] = named(p)
		f.gitTask(fmt.Sprintf("g%02d", i), repoURL, "prod", p, named(p))
	}
	gh.commit("prod", commitB, files)

	a := f.do("POST", previewAt, map[string]string{"url": repoURL, "branch": "prod", "path": ""})
	var p previewAnswer
	a.data(t, &p)
	for _, fl := range p.Files {
		if fl.State != "unchanged" {
			t.Errorf("%s is %s, want unchanged", fl.Path, fl.State)
		}
	}
	if len(p.Files) != 10 || len(p.Removed) != 0 {
		t.Errorf("preview = %+v, want 10 files and nothing removed", p)
	}
	if calls := gh.callLog(); len(calls) != 2 {
		t.Errorf("GitHub calls = %q, want 2", calls)
	}
}

func TestThePreviewOfOneFile(t *testing.T) {
	f, gh := newGitAPI(t)
	gh.commit("main", commitA, map[string]string{"tasks/web.yaml": named("web"), "tasks/db.yaml": named("db")})
	f.gitTask("db1", repoURL, "main", "tasks/db.yaml", named("db"))

	var p previewAnswer
	f.do("POST", previewAt, map[string]string{"url": repoURL, "path": "/tasks/web.yaml"}).data(t, &p)
	if p.Path != "tasks/web.yaml" || len(p.Files) != 1 || p.Files[0].Path != "tasks/web.yaml" || p.Files[0].State != "new" || len(p.Removed) != 0 {
		t.Errorf("preview = %+v, want tasks/web.yaml alone, new, and db.yaml not removed", p)
	}
}

func TestThePreviewRefusesWhatGitHubRefuses(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(gh *fakeGitHub)
		body    map[string]string
		status  int
		code    string
		details map[string]string
	}{
		{name: "more than 50 task files", setup: func(gh *fakeGitHub) {
			files := map[string]string{}
			for i := range 51 {
				files[fmt.Sprintf("t/%02d.yml", i)] = named(fmt.Sprint(i))
			}
			gh.commit("main", commitA, files)
		}, body: map[string]string{"url": repoURL, "path": "t"}, status: 400, code: response.ErrInvalidRequest, details: map[string]string{"max": "50", "found": "51"}},
		{name: "a missing repository", setup: func(gh *fakeGitHub) { gh.gone = true },
			body: map[string]string{"url": repoURL}, status: 404, code: response.ErrGitRepoNotFound},
		{name: "a missing branch", setup: func(gh *fakeGitHub) { gh.commit("main", commitA, map[string]string{"a.yaml": named("a")}) },
			body: map[string]string{"url": repoURL, "branch": "nope"}, status: 404, code: response.ErrGitPathNotFound},
		{name: "a missing folder", setup: func(gh *fakeGitHub) { gh.commit("main", commitA, map[string]string{"a.yaml": named("a")}) },
			body: map[string]string{"url": repoURL, "path": "nope"}, status: 404, code: response.ErrGitPathNotFound},
		{name: "a used-up allowance", setup: func(gh *fakeGitHub) {
			gh.before = func(w http.ResponseWriter, r *http.Request) bool {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Limit", "60")
				w.Header().Set("X-RateLimit-Reset", "1893456000")
				w.WriteHeader(http.StatusForbidden)
				return true
			}
		}, body: map[string]string{"url": repoURL}, status: 429, code: response.ErrGitRateLimited, details: map[string]string{"limit": "60", "reset_at": "2030-01-01T00:00:00Z"}},
		{name: "a refused token", setup: func(gh *fakeGitHub) {
			gh.before = func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(http.StatusUnauthorized); return true }
		}, body: map[string]string{"url": repoURL, "token": "ghp_wrong"}, status: 400, code: response.ErrGitAuthFailed},
		{name: "a URL that is not GitHub's", body: map[string]string{"url": "https://gitlab.example/acme/ops"}, status: 400, code: response.ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, gh := newGitAPI(t)
			if tc.setup != nil {
				tc.setup(gh)
			}
			wantRefusal(t, tc.name, f.do("POST", previewAt, tc.body), tc.status, tc.code, tc.details)
		})
	}
}

// importBody is an import of files from the tasks folder of acme/ops on main
// at commit.
func importBody(commit string, files ...string) map[string]any {
	return map[string]any{"url": repoURL, "path": "tasks", "commit": commit, "files": files}
}

// The import reads every file at the commit the preview showed: a push in
// between must not slip in content nobody looked at.
func TestAnImportReadsTheFilesAtThePreviewsCommit(t *testing.T) {
	f, gh := newGitAPI(t)
	gh.commit("main", commitA, map[string]string{"tasks/web.yaml": named("web"), "tasks/db.yml": named("db")})
	// Pushed after the preview: main has moved on.
	gh.commit("main", commitB, map[string]string{"tasks/web.yaml": named("web") + "# pushed later\n", "tasks/db.yml": named("db") + "# pushed later\n"})

	a := f.do("POST", importAt, importBody(commitA, "tasks/web.yaml", "tasks/db.yml"))
	if a.Status != 200 {
		t.Fatalf("import = %d %+v", a.Status, a.Error)
	}
	if strings.Contains(string(a.Data), `"yaml":`) || strings.Contains(string(a.Data), "steps") {
		t.Errorf("the answer carries documents: %s", a.Data)
	}
	var ans importAnswer
	a.data(t, &ans)
	if ans.Commit != commitA || len(ans.Failed) != 0 || len(ans.Imported) != 2 {
		t.Fatalf("answer = %+v, want two imported at %s", ans, commitA)
	}
	for _, call := range gh.callLog() {
		if !strings.HasPrefix(call, "file ") || !strings.HasSuffix(call, " "+commitA) {
			t.Errorf("GitHub call %q, want each file read at %s", call, commitA)
		}
	}
	for _, v := range ans.Imported {
		st, err := f.env.store.GetTask(v.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantSrc := cluster.TaskSource{Kind: cluster.TaskSourceGit, Repo: repoURL, Branch: "main", Path: "tasks/" + map[string]string{"web": "web.yaml", "db": "db.yml"}[st.Name], SHA: commitA}
		if st.YAML != named(st.Name) || st.Source != wantSrc || st.UpdatedBy != "operator" || !v.ReadOnly || v.Source != wantSrc {
			t.Errorf("stored %+v, view %+v; want the document at %s, from %+v, by the operator, read-only", st, v, commitA, wantSrc)
		}
	}

	// An imported task is changed by the next import, not here; it may go.
	id := ans.Imported[0].ID
	wantRefusal(t, "PUT an imported task", f.do("PUT", "/api/v1/cluster/tasks/"+id, map[string]string{"yaml": named("edited")}), 409, response.ErrTaskReadOnly, nil)
	if a := f.do("DELETE", "/api/v1/cluster/tasks/"+id, nil); a.Status != 200 {
		t.Errorf("DELETE an imported task = %d %+v, want 200", a.Status, a.Error)
	}
}

// An import that GitHub refuses as a whole writes nothing, though the files
// before the refusal were read.
func TestAnImportGitHubRefusesWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		refuse  func(w http.ResponseWriter)
		status  int
		code    string
		details map[string]string
	}{
		{name: "a refused token", refuse: func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }, status: 400, code: response.ErrGitAuthFailed},
		{name: "a used-up allowance", refuse: func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusForbidden)
		}, status: 429, code: response.ErrGitRateLimited, details: map[string]string{"retry_after": "120"}},
		{name: "a repository gone", refuse: func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }, status: 404, code: response.ErrGitRepoNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, gh := newGitAPI(t)
			gh.commit("main", commitA, map[string]string{"tasks/a.yaml": named("a"), "tasks/b.yaml": named("b"), "tasks/c.yaml": named("c")})
			reads := 0
			gh.before = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Header.Get("Accept") == "application/vnd.github.raw" {
					reads++
					if reads == 1 {
						return false
					}
				}
				tc.refuse(w)
				return true
			}
			a := f.do("POST", importAt, importBody(commitA, "tasks/a.yaml", "tasks/b.yaml", "tasks/c.yaml"))
			wantRefusal(t, tc.name, a, tc.status, tc.code, tc.details)
			if reads < 2 {
				t.Errorf("files read = %d, want the first read before the refusal", reads)
			}
			if n := len(f.env.store.tasks); n != 0 {
				t.Errorf("stored tasks = %d, want none", n)
			}
		})
	}
}

// utf16 is s as UTF-16LE with a byte-order mark, as some Windows editors
// save it.
func utf16LE(s string) string {
	b := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return string(b)
}

// Each file that cannot be a task is listed with why; the others are written.
func TestAnImportListsTheFilesItCouldNotTakeAndWritesTheRest(t *testing.T) {
	f, gh := newGitAPI(t)
	builtin := builtins()[0].Name
	gh.commit("main", commitA, map[string]string{
		"tasks/good.yaml":    named("good"),
		"tasks/utf16.yaml":   utf16LE(named("wide")),
		"tasks/bad.yaml":     "name: bad\nsteps:\n  - bogus: 1\n",
		"tasks/big.yaml":     named("big") + "#" + strings.Repeat("x", tasks.MaxDocumentBytes),
		"tasks/builtin.yaml": named(builtin),
		"tasks/taken.yaml":   named("mine"),
	})
	f.store("u1", named("mine"))

	a := f.do("POST", importAt, importBody(commitA, "tasks/good.yaml", "tasks/gone.yaml", "tasks/utf16.yaml", "tasks/bad.yaml", "tasks/big.yaml", "tasks/builtin.yaml", "tasks/taken.yaml"))
	if a.Status != 200 {
		t.Fatalf("import = %d %+v", a.Status, a.Error)
	}
	var ans importAnswer
	a.data(t, &ans)
	if len(ans.Imported) != 1 || ans.Imported[0].Name != "good" {
		t.Errorf("imported = %+v, want good alone", ans.Imported)
	}
	failed := map[string]response.Warning{}
	for _, w := range ans.Failed {
		failed[w.Details["path"]] = w
	}
	want := []struct {
		path, code, message string
		details             map[string]string
	}{
		{"tasks/gone.yaml", response.ErrGitPathNotFound, "", nil},
		{"tasks/utf16.yaml", response.ErrTaskInvalid, "not UTF-8", nil},
		{"tasks/bad.yaml", response.ErrTaskInvalid, "line 3:", map[string]string{"line": "3"}},
		{"tasks/big.yaml", response.ErrTaskTooLarge, "", map[string]string{"max_kib": "64"}},
		{"tasks/builtin.yaml", response.ErrTaskNameTaken, "built-in", nil},
		{"tasks/taken.yaml", response.ErrTaskNameTaken, "", nil},
	}
	for _, w := range want {
		got, ok := failed[w.path]
		if !ok || got.Code != w.code || !strings.Contains(got.Message, w.message) {
			t.Errorf("%s failed as %+v (%v), want %s with %q", w.path, got, ok, w.code, w.message)
		}
		for k, v := range w.details {
			if got.Details[k] != v {
				t.Errorf("%s: details[%s] = %q, want %q", w.path, k, got.Details[k], v)
			}
		}
	}
	if len(ans.Failed) != len(want) {
		t.Errorf("failed = %+v, want %d", ans.Failed, len(want))
	}
	if n := len(f.env.store.tasks); n != 2 {
		t.Errorf("stored tasks = %d, want u1 and good", n)
	}
}

// A blank name is the file's problem: the files around it are still written.
func TestAnImportListsAFileWithABlankNameAndWritesTheOthers(t *testing.T) {
	f, gh := newGitAPI(t)
	gh.commit("main", commitA, map[string]string{
		"tasks/a.yaml": named("first"),
		"tasks/b.yaml": named(`""`),
		"tasks/c.yaml": named("third"),
	})
	a := f.do("POST", importAt, importBody(commitA, "tasks/a.yaml", "tasks/b.yaml", "tasks/c.yaml"))
	if a.Status != 200 {
		t.Fatalf("import = %d %+v", a.Status, a.Error)
	}
	var ans importAnswer
	a.data(t, &ans)
	if len(ans.Imported) != 2 || ans.Imported[0].Name != "first" || ans.Imported[1].Name != "third" {
		t.Errorf("imported = %+v, want first and third", ans.Imported)
	}
	if len(ans.Failed) != 1 || ans.Failed[0].Code != response.ErrTaskInvalid ||
		ans.Failed[0].Details["path"] != "tasks/b.yaml" || !strings.Contains(ans.Failed[0].Message, "name is blank") {
		t.Errorf("failed = %+v, want tasks/b.yaml alone as %s, its name blank", ans.Failed, response.ErrTaskInvalid)
	}
	if n := len(f.env.store.tasks); n != 2 {
		t.Errorf("stored tasks = %d, want first and third", n)
	}
}

// The same repository, branch and path is the same task: a fresh import
// changes it in place and moves its commit.
func TestAReimportKeepsTheTaskAndMovesItsCommit(t *testing.T) {
	f, gh := newGitAPI(t)
	gh.commit("main", commitA, map[string]string{"tasks/web.yaml": named("web")})
	var first importAnswer
	f.do("POST", importAt, importBody(commitA, "tasks/web.yaml")).data(t, &first)
	if len(first.Imported) != 1 {
		t.Fatalf("first import = %+v", first)
	}
	id := first.Imported[0].ID

	gh.commit("main", commitB, map[string]string{"tasks/web.yaml": named("web") + "# v2\n"})
	body := importBody(commitB, "tasks/web.yaml")
	body["url"] = "https://github.com/Acme/Ops.git"
	var second importAnswer
	f.do("POST", importAt, body).data(t, &second)
	if len(second.Imported) != 1 || second.Imported[0].ID != id {
		t.Fatalf("second import = %+v, want task %s again", second, id)
	}
	st, _ := f.env.store.GetTask(id)
	if st.Source.SHA != commitB || st.YAML != named("web")+"# v2\n" || len(f.env.store.tasks) != 1 {
		t.Errorf("stored %+v (%d tasks), want task %s alone at %s with the new document", st, len(f.env.store.tasks), id, commitB)
	}

	// Another branch is another task, which may not take the same name.
	gh.commit("dev", commitA, map[string]string{"tasks/web.yaml": named("web")})
	body = importBody(commitA, "tasks/web.yaml")
	body["branch"] = "dev"
	var third importAnswer
	f.do("POST", importAt, body).data(t, &third)
	if len(third.Imported) != 0 || len(third.Failed) != 1 || third.Failed[0].Code != response.ErrTaskNameTaken {
		t.Errorf("import from dev = %+v, want web refused as a name taken", third)
	}
}

func TestAnImportRequestIsCheckedBeforeGitHubIsAsked(t *testing.T) {
	fifty1 := make([]string, 51)
	for i := range fifty1 {
		fifty1[i] = fmt.Sprintf("tasks/%02d.yaml", i)
	}
	cases := []struct {
		name    string
		change  func(b map[string]any)
		details map[string]string
	}{
		{name: "a commit that is not a SHA", change: func(b map[string]any) { b["commit"] = "main" }},
		{name: "a commit in capitals", change: func(b map[string]any) { b["commit"] = strings.ToUpper(commitA) }},
		{name: "no files", change: func(b map[string]any) { b["files"] = []string{} }},
		{name: "51 files", change: func(b map[string]any) { b["files"] = fifty1 }, details: map[string]string{"max": "50", "found": "51"}},
		{name: "a file twice", change: func(b map[string]any) { b["files"] = []string{"tasks/a.yaml", "tasks/a.yaml"} }},
		{name: "a file in another folder", change: func(b map[string]any) { b["files"] = []string{"other/a.yaml"} }},
		{name: "a file in a folder below", change: func(b map[string]any) { b["files"] = []string{"tasks/sub/a.yaml"} }},
		{name: "a file that is not YAML", change: func(b map[string]any) { b["files"] = []string{"tasks/a.txt"} }},
		{name: "a dot-dot path", change: func(b map[string]any) { b["path"] = ".."; b["files"] = []string{"../a.yaml"} }},
		{name: "a URL that is not GitHub's", change: func(b map[string]any) { b["url"] = "https://github.com/acme" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, gh := newGitAPI(t)
			gh.commit("main", commitA, map[string]string{"tasks/a.yaml": named("a")})
			body := importBody(commitA, "tasks/a.yaml")
			tc.change(body)
			wantRefusal(t, tc.name, f.do("POST", importAt, body), 400, response.ErrInvalidRequest, tc.details)
			if calls := gh.callLog(); len(calls) != 0 {
				t.Errorf("GitHub calls = %q, want none", calls)
			}
		})
	}
}

// The token is used for GitHub and nowhere else: not in the stored task,
// the answer or a log line.
func TestTheTokenIsNeitherKeptNorLogged(t *testing.T) {
	const token = "github_pat_SECRET0123456789"
	buf := panelLogs(t, "debug")
	f, gh := newGitAPI(t)
	gh.commit("main", commitA, map[string]string{"tasks/web.yaml": named("web"), "tasks/bad.yaml": "name: bad\n"})

	body := importBody(commitA, "tasks/web.yaml", "tasks/bad.yaml")
	body["token"] = token
	pv := f.do("POST", previewAt, map[string]string{"url": repoURL, "path": "tasks", "token": token})
	a := f.do("POST", importAt, body)
	if pv.Status != 200 || a.Status != 200 {
		t.Fatalf("preview %d, import %d %+v", pv.Status, a.Status, a.Error)
	}
	for _, auth := range gh.auth {
		if auth != "Bearer "+token {
			t.Errorf("a GitHub call carried Authorization %q, want the token", auth)
		}
	}
	stored, _ := json.Marshal(f.env.store.tasks)
	for what, s := range map[string]string{"stored tasks": string(stored), "preview": string(pv.Data), "answer": string(a.Data), "logs": buf.b.String()} {
		if strings.Contains(s, token) || strings.Contains(s, "SECRET") {
			t.Errorf("the token is in the %s: %s", what, s)
		}
	}
	var line map[string]any
	for _, rec := range buf.records(t) {
		if rec["msg"] == "tasks imported from git" {
			line = rec
		}
	}
	if line == nil || line["component"] != "tasks" || line["repo"] != repoURL || line["branch"] != "main" || line["commit"] != commitA ||
		line["imported"] != float64(1) || line["failed"] != float64(1) {
		t.Errorf("log record = %v, want the repo, branch, commit, 1 imported and 1 failed", line)
	}

	// A refusal neither.
	gh.before = func(w http.ResponseWriter, r *http.Request) bool { w.WriteHeader(http.StatusUnauthorized); return true }
	refused := f.do("POST", importAt, body)
	raw, _ := json.Marshal(refused.Error)
	if refused.code() != response.ErrGitAuthFailed || strings.Contains(string(raw), "SECRET") || strings.Contains(buf.b.String(), "SECRET") {
		t.Errorf("refusal %s, logs %s: want GIT_AUTH_FAILED without the token", raw, buf.b.String())
	}
}
