package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitdiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type SourceControlService struct {
	workspaceRoot string
}

type SourceControlStatus struct {
	HasRepository bool                  `json:"has_repository"`
	Branch        string                `json:"branch"`
	Clean         bool                  `json:"clean"`
	Changes       []SourceControlChange `json:"changes"`
}

type SourceControlChange struct {
	Path           string `json:"path"`
	StagedStatus   string `json:"staged_status"`
	WorktreeStatus string `json:"worktree_status"`
	Staged         bool   `json:"staged"`
}

type SourceControlCommit struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
}

type SourceControlDiff struct {
	Path     string `json:"path"`
	Staged   bool   `json:"staged"`
	Patch    string `json:"patch"`
	Original string `json:"original"`
	Modified string `json:"modified"`
	Binary   bool   `json:"binary"`
}

func NewSourceControlService(workspaceRoot string) *SourceControlService {
	return &SourceControlService{workspaceRoot: workspaceRoot}
}

func (s *SourceControlService) Status(ctx context.Context) (SourceControlStatus, error) {
	repo, worktree, err := s.open()
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return SourceControlStatus{HasRepository: false, Clean: true, Changes: []SourceControlChange{}}, nil
		}
		return SourceControlStatus{}, err
	}
	status, err := worktree.Status()
	if err != nil {
		return SourceControlStatus{}, err
	}
	result := SourceControlStatus{HasRepository: true, Clean: status.IsClean(), Changes: []SourceControlChange{}}
	result.Branch = currentBranch(repo)
	runtimePrefix := s.runtimeGitPathPrefix()
	for path, item := range status {
		staging := normalizeStatusCode(item.Staging)
		worktreeStatus := normalizeStatusCode(item.Worktree)
		if isRuntimeGitPath(path, runtimePrefix) &&
			(staging == git.Untracked || staging == git.Unmodified) && worktreeStatus == git.Untracked {
			continue
		}
		result.Changes = append(result.Changes, SourceControlChange{
			Path:           filepath.ToSlash(path),
			StagedStatus:   statusCodeString(staging),
			WorktreeStatus: statusCodeString(worktreeStatus),
			Staged:         isStagedStatus(staging),
		})
	}
	sort.Slice(result.Changes, func(i, j int) bool { return result.Changes[i].Path < result.Changes[j].Path })
	result.Clean = len(result.Changes) == 0
	_ = ctx
	return result, nil
}

func (s *SourceControlService) Diff(path string, staged bool) (SourceControlDiff, error) {
	cleaned, err := cleanGitPath(path)
	if err != nil {
		return SourceControlDiff{}, err
	}
	repo, worktree, err := s.open()
	if err != nil {
		return SourceControlDiff{}, err
	}
	var src, dst string
	if staged {
		src, _ = headFileContents(repo, cleaned)
		dst, _ = indexFileContents(repo, cleaned)
	} else {
		src, err = indexFileContents(repo, cleaned)
		if err != nil {
			src, _ = headFileContents(repo, cleaned)
		}
		dst, _ = worktreeFileContents(worktree, cleaned)
	}
	binary := isBinaryDiff(src, dst)
	patch := renderDiff(cleaned, src, dst)
	if binary {
		// Never send NUL-containing content into Monaco. The patch still carries
		// the user-facing binary-diff message.
		src = ""
		dst = ""
	}
	return SourceControlDiff{
		Path:     cleaned,
		Staged:   staged,
		Patch:    patch,
		Original: src,
		Modified: dst,
		Binary:   binary,
	}, nil
}

func (s *SourceControlService) Branches(ctx context.Context) ([]string, error) {
	repo, _, err := s.open()
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return []string{}, nil
		}
		return nil, err
	}
	iter, err := repo.Branches()
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	branches := []string{}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		branches = append(branches, ref.Name().Short())
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(branches)
	_ = ctx
	return branches, nil
}

func (s *SourceControlService) Init(ctx context.Context) (SourceControlStatus, error) {
	_, err := git.PlainInitWithOptions(s.workspaceRoot, &git.PlainInitOptions{
		InitOptions: git.InitOptions{
			DefaultBranch: plumbing.NewBranchReferenceName("main"),
		},
	})
	if err != nil {
		return SourceControlStatus{}, err
	}
	if err := s.writeDefaultGitignore(); err != nil {
		return SourceControlStatus{}, err
	}
	return s.Status(ctx)
}

func (s *SourceControlService) Stage(paths []string) error {
	_, worktree, err := s.open()
	if err != nil {
		return err
	}
	for _, path := range paths {
		cleaned, err := cleanGitPath(path)
		if err != nil {
			return err
		}
		// SkipStatus avoids go-git recomputing the whole worktree status on
		// every Add — without it, staging many files (e.g. "Stage all") is
		// O(files × repo size) and appears to hang. For deleted paths go-git
		// still falls back to a status-based add internally.
		if err := worktree.AddWithOptions(&git.AddOptions{Path: cleaned, SkipStatus: true}); err != nil {
			return err
		}
	}
	return nil
}

func (s *SourceControlService) Unstage(paths []string) error {
	repo, worktree, err := s.open()
	if err != nil {
		return err
	}
	files := make([]string, 0, len(paths))
	for _, path := range paths {
		cleaned, err := cleanGitPath(path)
		if err != nil {
			return err
		}
		files = append(files, cleaned)
	}
	if _, err := repo.Head(); err != nil {
		return removeIndexEntries(repo, files)
	}
	return worktree.Reset(&git.ResetOptions{Mode: git.MixedReset, Files: files})
}

func (s *SourceControlService) Checkout(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.Contains(branch, "..") || strings.ContainsAny(branch, "\x00\n\r") {
		return fmt.Errorf("invalid branch name")
	}
	_, worktree, err := s.open()
	if err != nil {
		return err
	}
	return worktree.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch)})
}

func (s *SourceControlService) Commit(message string) (SourceControlCommit, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return SourceControlCommit{}, fmt.Errorf("commit message is required")
	}
	repo, worktree, err := s.open()
	if err != nil {
		return SourceControlCommit{}, err
	}
	status, err := worktree.Status()
	if err != nil {
		return SourceControlCommit{}, err
	}
	staged := false
	for _, item := range status {
		if item.Staging != git.Unmodified {
			staged = true
			break
		}
	}
	if !staged {
		return SourceControlCommit{}, fmt.Errorf("no staged changes to commit")
	}
	author := commitAuthor(repo)
	hash, err := worktree.Commit(message, &git.CommitOptions{Author: author})
	if err != nil {
		return SourceControlCommit{}, err
	}
	return SourceControlCommit{Hash: hash.String(), Message: message}, nil
}

func (s *SourceControlService) open() (*git.Repository, *git.Worktree, error) {
	repo, err := git.PlainOpenWithOptions(s.workspaceRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, nil, err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return nil, nil, err
	}
	return repo, worktree, nil
}

const defaultGitignoreContents = `.renart/state.db*
.renart/server.lock
.renart/server.json*
.renart/scheduler.lock
.renart/execution.lock
.renart/runtime/
logs/
duckdb-files/
.env
__pycache__/
.DS_Store
`

func (s *SourceControlService) writeDefaultGitignore() error {
	path := filepath.Join(s.workspaceRoot, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(defaultGitignoreContents), 0o644)
}

var runtimeGitExcludePaths = []string{
	".renart/state.db*",
	".renart/server.lock",
	".renart/server.json*",
	".renart/scheduler.lock",
	".renart/execution.lock",
	".renart/runtime/",
}

// EnsureRuntimeGitExcludes keeps Renart-owned runtime files out of source
// control without modifying a user's .gitignore. The repository-local
// info/exclude file is deliberately untracked and applies to existing projects
// as well as newly scaffolded ones.
func EnsureRuntimeGitExcludes(workspaceRoot string) error {
	repositoryRoot, metadataDir, err := findRepositoryMetadata(workspaceRoot)
	if err != nil {
		return err
	}
	workspaceRoot, err = filepath.Abs(workspaceRoot)
	if err != nil {
		return err
	}
	relativeRoot, err := filepath.Rel(repositoryRoot, workspaceRoot)
	if err != nil || relativeRoot == ".." || strings.HasPrefix(relativeRoot, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace %q is outside repository %q", workspaceRoot, repositoryRoot)
	}

	prefix := ""
	if relativeRoot != "." {
		prefix, err = gitExcludeLiteralPath(filepath.ToSlash(relativeRoot))
		if err != nil {
			return err
		}
		prefix += "/"
	}
	patterns := make([]string, 0, len(runtimeGitExcludePaths))
	for _, path := range runtimeGitExcludePaths {
		patterns = append(patterns, "/"+prefix+path)
	}
	return appendMissingGitExcludePatterns(filepath.Join(metadataDir, "info", "exclude"), patterns)
}

func gitExcludeLiteralPath(path string) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("workspace path cannot be represented safely in Git excludes")
	}
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"?", "\\?",
		"[", "\\[",
		"]", "\\]",
	)
	return replacer.Replace(path), nil
}

func (s *SourceControlService) runtimeGitPathPrefix() string {
	repositoryRoot, _, err := findRepositoryMetadata(s.workspaceRoot)
	if err != nil {
		return ""
	}
	workspaceRoot, err := filepath.Abs(s.workspaceRoot)
	if err != nil {
		return ""
	}
	relativeRoot, err := filepath.Rel(repositoryRoot, workspaceRoot)
	if err != nil {
		return ""
	}
	prefix := ""
	if relativeRoot != "." {
		prefix = filepath.ToSlash(relativeRoot) + "/"
	}
	return prefix + ".renart/"
}

func isRuntimeGitPath(path, statePrefix string) bool {
	if statePrefix == "" {
		return false
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, statePrefix) {
		return false
	}
	name := strings.TrimPrefix(path, statePrefix)
	return strings.HasPrefix(name, "runtime/") || strings.HasPrefix(name, "state.db") ||
		strings.HasPrefix(name, "server.json") ||
		name == "server.lock" ||
		name == "scheduler.lock" ||
		name == "execution.lock"
}

func findRepositoryMetadata(workspaceRoot string) (string, string, error) {
	current, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	for {
		marker := filepath.Join(current, ".git")
		info, statErr := os.Stat(marker)
		switch {
		case statErr == nil && info.IsDir():
			return current, marker, nil
		case statErr == nil:
			metadataDir, resolveErr := resolveGitDirFile(marker)
			if resolveErr != nil {
				return "", "", resolveErr
			}
			return current, metadataDir, nil
		case !errors.Is(statErr, os.ErrNotExist):
			return "", "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", git.ErrRepositoryNotExists
		}
		current = parent
	}
}

func resolveGitDirFile(marker string) (string, error) {
	raw, err := os.ReadFile(marker)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	const prefix = "gitdir:"
	if !strings.HasPrefix(strings.ToLower(value), prefix) {
		return "", fmt.Errorf("invalid git metadata file %q", marker)
	}
	metadataDir := strings.TrimSpace(value[len(prefix):])
	if !filepath.IsAbs(metadataDir) {
		metadataDir = filepath.Join(filepath.Dir(marker), metadataDir)
	}
	metadataDir = filepath.Clean(metadataDir)

	// Linked worktrees point at .git/worktrees/<name>; their commondir points
	// back to the main repository metadata where info/exclude lives.
	commonRaw, err := os.ReadFile(filepath.Join(metadataDir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return metadataDir, nil
	}
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(commonRaw))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(metadataDir, commonDir)
	}
	return filepath.Clean(commonDir), nil
}

func appendMissingGitExcludePatterns(path string, patterns []string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	existing := make(map[string]struct{})
	for _, line := range strings.Split(string(raw), "\n") {
		existing[strings.TrimSuffix(line, "\r")] = struct{}{}
	}
	missing := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if _, found := existing[pattern]; !found {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}

	var addition strings.Builder
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		addition.WriteByte('\n')
	}
	for _, pattern := range missing {
		addition.WriteString(pattern)
		addition.WriteByte('\n')
	}
	if _, err := io.WriteString(file, addition.String()); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func commitAuthor(repo *git.Repository) *object.Signature {
	name := "Renart"
	email := "renart@localhost"
	if cfg, err := repo.Config(); err == nil && cfg.User.Name != "" && cfg.User.Email != "" {
		name = cfg.User.Name
		email = cfg.User.Email
	}
	return &object.Signature{Name: name, Email: email, When: time.Now()}
}

func currentBranch(repo *git.Repository) string {
	if head, err := repo.Head(); err == nil {
		return head.Name().Short()
	}
	ref, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil || ref.Type() != plumbing.SymbolicReference {
		return ""
	}
	return ref.Target().Short()
}

func cleanGitPath(path string) (string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("invalid path %q", path)
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("invalid path %q", path)
	}
	return cleaned, nil
}

func normalizeStatusCode(code git.StatusCode) git.StatusCode {
	if code == 0 {
		return git.Unmodified
	}
	return code
}

func statusCodeString(code git.StatusCode) string {
	code = normalizeStatusCode(code)
	if code == git.Unmodified {
		return ""
	}
	return string(code)
}

func isStagedStatus(code git.StatusCode) bool {
	code = normalizeStatusCode(code)
	return code != git.Unmodified && code != git.Untracked
}

func headFileContents(repo *git.Repository, path string) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}
	file, err := tree.File(path)
	if err != nil {
		return "", err
	}
	return file.Contents()
}

func indexFileContents(repo *git.Repository, path string) (string, error) {
	idx, err := repo.Storer.Index()
	if err != nil {
		return "", err
	}
	entry, err := idx.Entry(path)
	if err != nil {
		return "", err
	}
	if entry.Mode == 0 {
		return "", nil
	}
	blob, err := object.GetBlob(repo.Storer, entry.Hash)
	if err != nil {
		return "", err
	}
	reader, err := blob.Reader()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	bytes, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func worktreeFileContents(worktree *git.Worktree, path string) (string, error) {
	file, err := worktree.Filesystem.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

func removeIndexEntries(repo *git.Repository, paths []string) error {
	idx, err := repo.Storer.Index()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := idx.Entry(path); err != nil {
			continue
		}
		_, _ = idx.Remove(path)
	}
	return repo.Storer.SetIndex(idx)
}

func renderDiff(path, src, dst string) string {
	if src == dst {
		return ""
	}
	if isBinaryDiff(src, dst) {
		return "Binary diff not shown."
	}
	var builder strings.Builder
	builder.WriteString("--- a/")
	builder.WriteString(path)
	builder.WriteString("\n+++ b/")
	builder.WriteString(path)
	builder.WriteString("\n")
	for _, item := range gitdiff.Do(src, dst) {
		prefix := " "
		if item.Type == diffmatchpatch.DiffInsert {
			prefix = "+"
		} else if item.Type == diffmatchpatch.DiffDelete {
			prefix = "-"
		}
		for _, line := range splitDiffLines(item.Text) {
			builder.WriteString(prefix)
			builder.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				builder.WriteString("\n")
			}
		}
	}
	return builder.String()
}

func isBinaryDiff(src, dst string) bool {
	return strings.Contains(src, "\x00") || strings.Contains(dst, "\x00")
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
