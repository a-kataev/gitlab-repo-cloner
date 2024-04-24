package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/spf13/pflag"
	"github.com/xanzy/go-gitlab"
)

type RepoCloner struct {
	destDir          string
	client           *gitlab.Client
	auth             transport.AuthMethod
	ignoreProjectIDs []int
	ignoreGroupIDs   []int
	progress         io.Writer
}

var listOptions = gitlab.ListOptions{
	PerPage: 1000,
	OrderBy: "name",
	Sort:    "asc",
}

func (rc *RepoCloner) Group(groupID int) {
	log := slog.With(slog.Int("group_id", groupID))

	if slices.Contains(rc.ignoreGroupIDs, groupID) {
		log.Warn("ignore group")

		return
	}

	group, _, err := rc.client.Groups.GetGroup(
		groupID,
		&gitlab.GetGroupOptions{
			ListOptions: listOptions,
		},
	)
	if err != nil {
		log.Error("get group error", slog.String("error", err.Error()))

		return
	}

	log = log.With(slog.String("group", group.FullPath))

	log.Info("get group repos")

	projects, _, err := rc.client.Groups.ListGroupProjects(
		group.ID,
		&gitlab.ListGroupProjectsOptions{
			ListOptions: listOptions,
		},
	)
	if err != nil {
		log.Error("list projects error", slog.String("error", err.Error()))

		return
	}

	for _, project := range projects {
		rc.gitClone(project, group.FullPath)
	}

	groups, _, err := rc.client.Groups.ListSubGroups(
		group.ID,
		&gitlab.ListSubGroupsOptions{
			ListOptions: listOptions,
		},
	)
	if err != nil {
		log.Error("list subgroups error", slog.String("error", err.Error()))

		return
	}

	for _, group := range groups {
		rc.Group(group.ID)
	}
}

func (rc *RepoCloner) Project(projectID int) {
	log := slog.With(slog.Int("project_id", projectID))

	if slices.Contains(rc.ignoreProjectIDs, projectID) {
		log.Warn("ignore project")

		return
	}

	project, _, err := rc.client.Projects.GetProject(
		projectID,
		&gitlab.GetProjectOptions{},
	)
	if err != nil {
		log.Error("get project error", slog.String("error", err.Error()))

		return
	}

	rc.gitClone(project, "")
}

func (rc *RepoCloner) gitClone(project *gitlab.Project, dest string) {
	subPath := path.Join(dest, project.Path)

	log := slog.With(slog.Int("project_id", project.ID), slog.String("path", subPath))

	log.Info("get repo")

	subPath = path.Join(rc.destDir, subPath)

	_, err := git.PlainClone(
		subPath,
		false,
		&git.CloneOptions{
			URL:      project.SSHURLToRepo,
			Auth:     rc.auth,
			Progress: rc.progress,
		},
	)
	if err != nil && !errors.Is(err, git.ErrRepositoryAlreadyExists) {
		log.Error("clone repo error", slog.String("error", err.Error()))

		return
	}

	repo, err := git.PlainOpen(subPath)
	if err != nil {
		log.Error("open repo error", slog.String("error", err.Error()))

		return
	}

	work, err := repo.Worktree()
	if err != nil {
		log.Error("worktree repo error", slog.String("error", err.Error()))

		return
	}

	err = work.Pull(
		&git.PullOptions{
			RemoteName: "origin",
			Force:      true,
			Progress:   rc.progress,
		},
	)
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		log.Error("pull repo error", slog.String("error", err.Error()))

		return
	}
}

func main() {
	currentDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		slog.Error("directory error", slog.String("error", err.Error()))

		os.Exit(1)
	}

	rc := &RepoCloner{
		destDir:          path.Join(currentDir, "repos"),
		ignoreProjectIDs: []int{},
		ignoreGroupIDs:   []int{},
		progress:         io.Discard,
	}

	gitlabHost := "https://gitlab.com"
	gitlabToken := ""
	groupIDs := []int{}
	projectIDs := []int{}
	progress := false

	flag := pflag.NewFlagSet(path.Base(os.Args[0]), pflag.ContinueOnError)

	flag.StringVar(&rc.destDir, "dest-dir", "./repos", "")
	flag.IntSliceVar(&rc.ignoreProjectIDs, "ignore-project-ids", rc.ignoreProjectIDs, "")
	flag.IntSliceVar(&rc.ignoreGroupIDs, "ignore-group-ids", rc.ignoreGroupIDs, "")
	flag.StringVar(&gitlabHost, "gitlab-host", gitlabHost, "")
	flag.StringVar(&gitlabToken, "gitlab-token", gitlabToken, "")
	flag.IntSliceVar(&groupIDs, "group-ids", groupIDs, "")
	flag.IntSliceVar(&projectIDs, "project-ids", projectIDs, "")
	flag.BoolVar(&progress, "progress", progress, "")

	if err := flag.Parse(os.Args[1:]); err != nil {
		if !errors.Is(pflag.ErrHelp, err) {
			slog.Error("flag error", slog.String("error", err.Error()))
		}

		os.Exit(1)
	}

	if progress {
		rc.progress = os.Stdout
	}

	client, err := gitlab.NewClient(
		gitlabToken,
		gitlab.WithBaseURL(gitlabHost+"/api/v4"),
	)
	if err != nil {
		slog.Error("client error", slog.String("error", err.Error()))

		os.Exit(1)
	}

	if _, _, err := client.Users.CurrentUser(); err != nil {
		slog.Error("current user error", slog.String("error", err.Error()))

		os.Exit(1)
	}

	rc.client = client

	auth, err := ssh.NewSSHAgentAuth("git")
	if err != nil {
		slog.Error("auth error", slog.String("error", err.Error()))

		os.Exit(1)
	}

	rc.auth = auth

	for _, gid := range groupIDs {
		rc.Group(gid)
	}

	for _, pid := range projectIDs {
		rc.Project(pid)
	}
}
