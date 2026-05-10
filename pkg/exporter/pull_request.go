package exporter

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/go-github/v85/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/promhippie/github_exporter/pkg/config"
	"github.com/promhippie/github_exporter/pkg/store"
	"github.com/ryanuber/go-glob"
)

// PRCollector collects per-repo pull request metrics.
type PRCollector struct {
	client   *github.Client
	logger   *slog.Logger
	db       store.Store
	failures *prometheus.CounterVec
	duration *prometheus.HistogramVec
	config   config.Target

	Open *prometheus.Desc
	Age  *prometheus.Desc
}

// NewPRCollector returns a new PRCollector.
func NewPRCollector(logger *slog.Logger, client *github.Client, db store.Store, failures *prometheus.CounterVec, duration *prometheus.HistogramVec, cfg config.Target) *PRCollector {
	if failures != nil {
		failures.WithLabelValues("pull_request").Add(0)
	}

	return &PRCollector{
		client:   client,
		logger:   logger.With("collector", "pull_request"),
		db:       db,
		failures: failures,
		duration: duration,
		config:   cfg,

		Open: prometheus.NewDesc(
			"github_repo_pull_requests_open",
			"Number of open pull requests by author type and draft status",
			[]string{"owner", "repo", "author_type", "draft"},
			nil,
		),
		Age: prometheus.NewDesc(
			"github_repo_pull_request_age_seconds",
			"Number of open pull requests by author type and non-overlapping age bucket",
			[]string{"owner", "repo", "author_type", "age_bucket"},
			nil,
		),
	}
}

// Metrics returns the list of metric descriptors for documentation generation.
func (c *PRCollector) Metrics() []*prometheus.Desc {
	return []*prometheus.Desc{
		c.Open,
		c.Age,
	}
}

// Describe sends the metric descriptors to the channel.
func (c *PRCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.Open
	ch <- c.Age
}

// Collect fetches open PR counts per repo and emits them as Prometheus metrics.
func (c *PRCollector) Collect(ch chan<- prometheus.Metric) {
	collected := make([]string, 0)

	for _, name := range c.config.Repos {
		parts := strings.SplitN(name, "/", 2)

		if len(parts) != 2 {
			c.logger.Error("Invalid repo name",
				"name", name,
			)

			c.failures.WithLabelValues("pull_request").Inc()
			continue
		}

		owner, repoPattern := parts[0], parts[1]

		ctx, cancel := context.WithTimeout(context.Background(), c.config.Timeout)
		defer cancel()

		now := time.Now()
		repos, err := reposByOwnerAndName(ctx, c.client, owner, repoPattern, c.config.PerPage)
		c.duration.WithLabelValues("pull_request").Observe(time.Since(now).Seconds())

		if err != nil {
			c.logger.Error("Failed to fetch repos",
				"name", name,
				"err", err,
			)

			c.failures.WithLabelValues("pull_request").Inc()
			continue
		}

		for _, repo := range repos {
			fullName := repo.GetFullName()

			if !glob.Glob(name, fullName) {
				continue
			}

			if alreadyCollected(collected, fullName) {
				c.logger.Debug("Already collected pull requests",
					"repo", fullName,
				)

				continue
			}

			collected = append(collected, fullName)

			c.logger.Debug("Collecting pull requests",
				"repo", fullName,
			)

			c.collectRepo(ctx, ch, owner, repo.GetName())
		}
	}
}

type openKey struct {
	authorType string
	draft      string
}

type ageKey struct {
	authorType string
	ageBucket  string
}

func (c *PRCollector) collectRepo(ctx context.Context, ch chan<- prometheus.Metric, owner, repo string) {
	opts := &github.PullRequestListOptions{
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: c.config.PerPage,
		},
	}

	openCounts := make(map[openKey]float64)
	ageCounts := make(map[ageKey]float64)

	for {
		prs, resp, err := c.client.PullRequests.List(ctx, owner, repo, opts)

		if err != nil {
			closeBody(resp)

			c.logger.Error("Failed to fetch pull requests",
				"owner", owner,
				"repo", repo,
				"err", err,
			)

			c.failures.WithLabelValues("pull_request").Inc()
			return
		}

		for _, pr := range prs {
			authorType := prAuthorType(pr.GetUser().GetLogin())
			draft := boolToString(pr.GetDraft())
			bucket := prAgeBucket(time.Since(pr.GetCreatedAt().Time))

			openCounts[openKey{authorType, draft}]++
			ageCounts[ageKey{authorType, bucket}]++
		}

		if resp.NextPage == 0 {
			closeBody(resp)
			break
		}

		closeBody(resp)
		opts.Page = resp.NextPage
	}

	for k, count := range openCounts {
		ch <- prometheus.MustNewConstMetric(
			c.Open,
			prometheus.GaugeValue,
			count,
			owner, repo, k.authorType, k.draft,
		)
	}

	for k, count := range ageCounts {
		ch <- prometheus.MustNewConstMetric(
			c.Age,
			prometheus.GaugeValue,
			count,
			owner, repo, k.authorType, k.ageBucket,
		)
	}
}

func prAuthorType(login string) string {
	lower := strings.ToLower(login)

	if strings.Contains(lower, "renovate") {
		return "renovate"
	}

	if strings.Contains(lower, "dependabot") {
		return "dependabot"
	}

	return "human"
}

func prAgeBucket(age time.Duration) string {
	switch {
	case age < 24*time.Hour:
		return "<1d"
	case age < 7*24*time.Hour:
		return "1d-7d"
	case age < 30*24*time.Hour:
		return "7d-30d"
	default:
		return ">30d"
	}
}

func boolToString(val bool) string {
	if val {
		return "true"
	}

	return "false"
}
