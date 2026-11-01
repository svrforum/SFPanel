package featuretasks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/githubapi"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// Importing task documents from a GitHub folder (design §5). The leader does
// both halves, as it does every task write: one place calls GitHub, against
// the tasks it holds, so one allowance is spent and the preview sorts the
// files against the same store the import writes to. A follower sends both on
// (forward); the forwarded answer is capped near 4 MiB, which is why neither
// answer carries a document.
//
// The token, when there is one, comes in the body and goes to GitHub in the
// Authorization header, and nowhere else: not into the task's source, a log
// line, an answer or an error. A private repository asks for it again on the
// next refresh.

// maxImportFiles is how many task files one folder may offer, and one import
// take: 50 reads plus the preview's 2 fit in GitHub's 60 an hour without a
// token.
const maxImportFiles = 50

// How long each route may take, GitHub calls included (each also has its own
// githubapi.CallTimeout). Both stay inside the proxy's five minutes for these
// paths and the browser's long-operation wait.
const (
	previewTimeout = 60 * time.Second
	importTimeout  = 2 * time.Minute
)

// gitImportSource is the folder an import reads: the repository, the branch
// and the path in it, "" for the root, a folder, or one .yaml/.yml file.
type gitImportSource struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Token  string `json:"token"`
}

// gitImportRequest is an import: the files the preview offered and the
// operator picked, read at the commit it showed.
type gitImportRequest struct {
	gitImportSource
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
}

// The states a preview gives a task file.
const (
	fileNew       = "new"
	fileChanged   = "changed"
	fileUnchanged = "unchanged"
	fileTooLarge  = "too_large"
)

// previewFile is one task file of the folder, and how it stands against the
// task imported from it, if any.
type previewFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	TaskID   string `json:"task_id,omitempty"`
	TaskName string `json:"task_name,omitempty"`
}

// removedTask is a task imported from the folder whose file is gone from it.
// The import leaves it; the screen offers the delete.
type removedTask struct {
	TaskID   string `json:"task_id"`
	TaskName string `json:"task_name"`
	Path     string `json:"path"`
}

type importPreview struct {
	Repo    string        `json:"repo"`
	Branch  string        `json:"branch"`
	Path    string        `json:"path"`
	Commit  string        `json:"commit"`
	Files   []previewFile `json:"files"`
	Removed []removedTask `json:"removed"`
}

type importResult struct {
	Commit   string             `json:"commit"`
	Imported []taskView         `json:"imported"`
	Failed   []response.Warning `json:"failed"`
}

var commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// readSource reads the repository and fills in the branch, main when left
// out as compose's import does. The path loses one leading or trailing
// slash; githubapi checks its segments before anything is sent.
func readSource(s *gitImportSource) (githubapi.Repo, error) {
	repo, err := githubapi.ParseRepoURL(strings.TrimSpace(s.URL))
	if err != nil {
		return repo, err
	}
	s.Branch = strings.TrimSpace(s.Branch)
	if s.Branch == "" {
		s.Branch = "main"
	}
	s.Path = strings.Trim(s.Path, "/")
	return repo, nil
}

// isTaskFile reports whether a repository path is a task document by name.
func isTaskFile(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".yaml" || ext == ".yml"
}

// folderOf is the folder a repository path is in, "" for the root.
func folderOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// importedFrom are the stored tasks imported from repo on branch, by their
// path. GitHub's owner and repository names are not case-sensitive.
func importedFrom(stored []cluster.StoredTask, repo githubapi.Repo, branch string) map[string]cluster.StoredTask {
	out := map[string]cluster.StoredTask{}
	for _, t := range stored {
		if t.Source.Kind == cluster.TaskSourceGit && strings.EqualFold(t.Source.Repo, repo.URL()) && t.Source.Branch == branch {
			out[t.Source.Path] = t
		}
	}
	return out
}

// gitRefusal answers an error from GitHub.
func gitRefusal(c echo.Context, err error) error {
	status, code, msg, details := githubapi.Refusal(err)
	if status == http.StatusInternalServerError {
		logger().Warn("task import: GitHub call failed", "error", err)
	}
	return response.FailWith(c, status, code, msg, details)
}

// tooMany refuses more than maxImportFiles task files.
func tooMany(c echo.Context, found int) error {
	return response.FailWith(c, http.StatusBadRequest, response.ErrInvalidRequest,
		fmt.Sprintf("At most %d task files are imported at once; found %d", maxImportFiles, found),
		map[string]string{"max": strconv.Itoa(maxImportFiles), "found": strconv.Itoa(found)})
}

// PreviewImport answers POST /cluster/tasks/import/preview {url, branch,
// path, token?} (leader): the branch's commit now, and each task file of the
// folder at that commit, sorted against the tasks imported from it by git
// blob SHA, so nothing is read but the listing: new, changed, unchanged, or
// too_large to import. Tasks imported from the folder whose file is gone are
// removed. A folder is read one level deep. It writes nothing, and asks
// GitHub twice (once more to tell a missing repository from a missing
// branch).
func (h *Handler) PreviewImport(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	var req gitImportSource
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	repo, err := readSource(&req)
	if err != nil {
		return gitRefusal(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), previewTimeout)
	defer cancel()

	commit, err := githubapi.ResolveRef(ctx, repo, req.Branch, req.Token)
	if errors.Is(err, githubapi.ErrPathNotFound) {
		// GitHub answers a repository the caller cannot see as it answers
		// a branch that is not there.
		if ok, perr := githubapi.RepoExists(ctx, repo, req.Token); perr != nil {
			err = perr
		} else if !ok {
			err = githubapi.ErrRepoNotFound
		}
	}
	if err != nil {
		return gitRefusal(c, err)
	}
	entries, err := githubapi.ListDir(ctx, repo, commit, req.Path, req.Token)
	if err != nil {
		return gitRefusal(c, err)
	}
	// A path naming a file lists that file alone, under its own path.
	oneFile := len(entries) == 1 && entries[0].Path == req.Path
	var candidates []githubapi.Entry
	for _, e := range entries {
		if e.Type == "file" && isTaskFile(e.Path) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) > maxImportFiles {
		return tooMany(c, len(candidates))
	}

	stored, err := env.Tasks().ListTasks()
	if err != nil {
		return fail(c, err)
	}
	mine := importedFrom(stored, repo, req.Branch)
	out := importPreview{Repo: repo.URL(), Branch: req.Branch, Path: req.Path, Commit: commit,
		Files: make([]previewFile, 0, len(candidates)), Removed: []removedTask{}}
	listed := map[string]bool{}
	for _, e := range candidates {
		listed[e.Path] = true
		f := previewFile{Path: e.Path, Name: e.Name, SHA: e.SHA, Size: e.Size}
		t, ok := mine[e.Path]
		if ok {
			f.TaskID, f.TaskName = t.ID, t.Name
		}
		switch {
		case e.Size > tasks.MaxDocumentBytes:
			f.State = fileTooLarge
		case !ok:
			f.State = fileNew
		case githubapi.BlobSHA([]byte(t.YAML)) == e.SHA:
			f.State = fileUnchanged
		default:
			f.State = fileChanged
		}
		out.Files = append(out.Files, f)
	}
	for p, t := range mine {
		inScope := folderOf(p) == req.Path
		if oneFile {
			inScope = p == req.Path
		}
		if inScope && !listed[p] {
			out.Removed = append(out.Removed, removedTask{TaskID: t.ID, TaskName: t.Name, Path: p})
		}
	}
	// By path, not in the map's order.
	slices.SortFunc(out.Removed, func(a, b removedTask) int { return strings.Compare(a.Path, b.Path) })
	return response.OK(c, out)
}

// checkImport refuses a request that names no commit, or files the preview
// of its path could not have offered: none, over 50, one twice, one not a
// .yaml/.yml directly in the folder (or not the file itself).
func checkImport(c echo.Context, req *gitImportRequest) (bool, error) {
	bad := func(msg string) (bool, error) {
		return false, response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, msg)
	}
	if !commitRe.MatchString(req.Commit) {
		return bad("commit must be the 40-character commit SHA the preview answered")
	}
	if len(req.Files) == 0 {
		return bad("files must name at least one task file")
	}
	if len(req.Files) > maxImportFiles {
		return false, tooMany(c, len(req.Files))
	}
	seen := map[string]bool{}
	for i, f := range req.Files {
		f = strings.Trim(f, "/")
		req.Files[i] = f
		if seen[f] {
			return bad("files names " + f + " twice")
		}
		seen[f] = true
		if !isTaskFile(f) || (f != req.Path && folderOf(f) != req.Path) {
			return bad("files must be .yaml or .yml files directly in path")
		}
	}
	return true, nil
}

// Import answers POST /cluster/tasks/import {url, branch, path, token?,
// commit, files} (leader): each file read at the commit the preview showed,
// so a push in between cannot slip in content nobody looked at, then each
// that is a task written as one imported from Git. Every file is read before
// anything is written: a refused token, a used-up allowance or a missing
// repository refuses the whole import. A file that cannot be a task is
// listed in failed with its code; the others are written. The same
// repository, branch and path is the same task, changed in place.
//
// Each task is its own write, so a leader lost partway leaves the files
// written so far; importing again rewrites them under the same IDs.
func (h *Handler) Import(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	if handled, err := h.forward(c, env); handled {
		return err
	}
	var req gitImportRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	repo, err := readSource(&req.gitImportSource)
	if err != nil {
		return gitRefusal(c, err)
	}
	if ok, err := checkImport(c, &req); !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), importTimeout)
	defer cancel()

	out := importResult{Commit: req.Commit, Imported: []taskView{}, Failed: []response.Warning{}}
	failed := func(p, code, msg string, details map[string]string) {
		d := map[string]string{"path": p}
		for k, v := range details {
			d[k] = v
		}
		out.Failed = append(out.Failed, response.Warning{Code: code, Message: msg, Details: d})
	}

	// Read every file first, one after another.
	docs := map[string][]byte{}
	repoSeen := false
	for _, p := range req.Files {
		body, err := githubapi.FetchFile(ctx, repo, req.Commit, p, req.Token, tasks.MaxDocumentBytes)
		switch {
		case err == nil:
			docs[p] = body
			continue
		case errors.Is(err, githubapi.ErrTooLarge):
			failed(p, response.ErrTaskTooLarge, cluster.ErrTaskTooLarge.Error(), map[string]string{"max_kib": strconv.Itoa(tasks.MaxDocumentBytes >> 10)})
			continue
		case errors.Is(err, githubapi.ErrPathNotFound):
			// A missing repository answers every path as missing: ask
			// once whether it is there.
			if !repoSeen {
				ok, perr := githubapi.RepoExists(ctx, repo, req.Token)
				if perr != nil {
					return gitRefusal(c, perr)
				}
				if !ok {
					return gitRefusal(c, githubapi.ErrRepoNotFound)
				}
				repoSeen = true
			}
			failed(p, response.ErrGitPathNotFound, "Nothing at this path at the commit", nil)
			continue
		}
		return gitRefusal(c, err)
	}

	stored, err := env.Tasks().ListTasks()
	if err != nil {
		return fail(c, err)
	}
	mine := importedFrom(stored, repo, req.Branch)
	for _, p := range req.Files {
		body, ok := docs[p]
		if !ok {
			continue
		}
		// Kept as read, byte for byte, so its blob SHA is the file's.
		// Stored as JSON, which would quietly rewrite text that is not
		// UTF-8 (a file saved as UTF-16 parses).
		if !utf8.Valid(body) {
			failed(p, response.ErrTaskInvalid, "The file is not UTF-8 text", nil)
			continue
		}
		t, problems := tasks.Parse(body)
		if t == nil {
			msg, details := "The task document has problems", map[string]string{}
			if len(problems) > 0 {
				msg = fmt.Sprintf("line %d: %s", problems[0].Line, problems[0].Message)
				details["line"] = strconv.Itoa(problems[0].Line)
			}
			failed(p, response.ErrTaskInvalid, msg, details)
			continue
		}
		if builtinName(t.Name) {
			failed(p, response.ErrTaskNameTaken, "A built-in task already has this name", nil)
			continue
		}
		id := cluster.NewTaskID()
		if prev, ok := mine[p]; ok {
			id = prev.ID
		}
		st := cluster.StoredTask{ID: id, Name: t.Name, YAML: string(body),
			Source:    cluster.TaskSource{Kind: cluster.TaskSourceGit, Repo: repo.URL(), Branch: req.Branch, Path: p, SHA: req.Commit},
			UpdatedAt: env.Now().UTC(), UpdatedBy: operator(c)}
		if err := env.Tasks().PutTask(st); err != nil {
			var ae *apiError
			if errors.As(storeError(err), &ae) {
				failed(p, ae.code, ae.message, ae.details)
				continue
			}
			return fail(c, err)
		}
		out.Imported = append(out.Imported, viewOf(taskDoc{ID: st.ID, Name: st.Name, YAML: st.YAML, Source: st.Source, UpdatedAt: st.UpdatedAt, UpdatedBy: st.UpdatedBy}, false))
	}
	logger().Info("tasks imported from git", "repo", repo.URL(), "branch", req.Branch, "commit", req.Commit,
		"imported", len(out.Imported), "failed", len(out.Failed))
	return response.OK(c, out)
}
