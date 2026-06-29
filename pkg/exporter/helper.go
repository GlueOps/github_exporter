package exporter

import (
	"context"
	"strings"

	"github.com/google/go-github/v88/github"
)

func closeBody(resp *github.Response) {
	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
	}
}

func alreadyCollected(collected []string, needle string) bool {
	for _, val := range collected {
		if needle == val {
			return true
		}
	}

	return false
}

func boolToFloat64(val bool) float64 {
	if val {
		return 1.0
	}

	return 0.0
}

func reposByOwnerAndName(ctx context.Context, client *github.Client, owner, repo string, perPage int) ([]*github.Repository, error) {
	if strings.Contains(repo, "*") {
		orgOpts := &github.RepositoryListByOrgOptions{
			ListOptions: github.ListOptions{
				PerPage: perPage,
			},
		}

		var repos []*github.Repository

		for {
			result, resp, err := client.Repositories.ListByOrg(ctx, owner, orgOpts)

			if err != nil {
				closeBody(resp)

				if resp != nil && resp.StatusCode == 404 {
					return reposByUser(ctx, client, owner, perPage)
				}

				return nil, err
			}

			repos = append(repos, result...)

			if resp.NextPage == 0 {
				closeBody(resp)
				break
			}

			closeBody(resp)
			orgOpts.Page = resp.NextPage
		}

		return repos, nil
	}

	res, _, err := client.Repositories.Get(ctx, owner, repo)

	if err != nil {
		return nil, err
	}

	return []*github.Repository{res}, nil
}

func reposByUser(ctx context.Context, client *github.Client, owner string, perPage int) ([]*github.Repository, error) {
	opts := &github.RepositoryListByUserOptions{
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	var repos []*github.Repository

	for {
		result, resp, err := client.Repositories.ListByUser(ctx, owner, opts)

		if err != nil {
			closeBody(resp)
			return nil, err
		}

		repos = append(repos, result...)

		if resp.NextPage == 0 {
			closeBody(resp)
			break
		}

		closeBody(resp)
		opts.Page = resp.NextPage
	}

	return repos, nil
}
