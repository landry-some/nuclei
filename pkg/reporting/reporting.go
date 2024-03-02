package reporting

import (
	"os"

	"github.com/projectdiscovery/nuclei/v3/pkg/catalog/config"
	json_exporter "github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/jsonexporter"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/jsonl"

	"go.uber.org/multierr"
	"gopkg.in/yaml.v2"

	"errors"

	"github.com/projectdiscovery/nuclei/v3/pkg/model/types/stringslice"
	"github.com/projectdiscovery/nuclei/v3/pkg/output"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/dedupe"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/es"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/markdown"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/sarif"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/exporters/splunk"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/trackers/filters"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/trackers/gitea"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/trackers/github"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/trackers/gitlab"
	"github.com/projectdiscovery/nuclei/v3/pkg/reporting/trackers/jira"
	errorutil "github.com/projectdiscovery/utils/errors"
	fileutil "github.com/projectdiscovery/utils/file"
)

var (
	ErrReportingClientCreation = errors.New("could not create reporting client")
	ErrExportClientCreation    = errors.New("could not create exporting client")
)

// Tracker is an interface implemented by an issue tracker
type Tracker interface {
	// CreateIssue creates an issue in the tracker
	CreateIssue(event *output.ResultEvent) error
	// ShouldFilter determines if the event should be filtered out
	ShouldFilter(event *output.ResultEvent) bool
}

// Exporter is an interface implemented by an issue exporter
type Exporter interface {
	// Close closes the exporter after operation
	Close() error
	// Export exports an issue to an exporter
	Export(event *output.ResultEvent) error
}

// ReportingClient is a client for nuclei issue tracking module
type ReportingClient struct {
	trackers  []Tracker
	exporters []Exporter
	options   *Options
	dedupe    *dedupe.Storage
}

// New creates a new nuclei issue tracker reporting client
func New(options *Options, db string) (Client, error) {
	client := &ReportingClient{options: options}

	if options.GitHub != nil {
		options.GitHub.HttpClient = options.HttpClient
		options.GitHub.OmitRaw = options.OmitRaw
		tracker, err := github.New(options.GitHub)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrReportingClientCreation)
		}
		client.trackers = append(client.trackers, tracker)
	}
	if options.GitLab != nil {
		options.GitLab.HttpClient = options.HttpClient
		options.GitLab.OmitRaw = options.OmitRaw
		tracker, err := gitlab.New(options.GitLab)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrReportingClientCreation)
		}
		client.trackers = append(client.trackers, tracker)
	}
	if options.Gitea != nil {
		options.Gitea.HttpClient = options.HttpClient
		options.Gitea.OmitRaw = options.OmitRaw
		tracker, err := gitea.New(options.Gitea)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrReportingClientCreation)
		}
		client.trackers = append(client.trackers, tracker)
	}
	if options.Jira != nil {
		options.Jira.HttpClient = options.HttpClient
		options.Jira.OmitRaw = options.OmitRaw
		tracker, err := jira.New(options.Jira)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrReportingClientCreation)
		}
		client.trackers = append(client.trackers, tracker)
	}
	if options.MarkdownExporter != nil {
		exporter, err := markdown.New(options.MarkdownExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}
	if options.SarifExporter != nil {
		exporter, err := sarif.New(options.SarifExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}
	if options.JSONExporter != nil {
		exporter, err := json_exporter.New(options.JSONExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}
	if options.JSONLExporter != nil {
		exporter, err := jsonl.New(options.JSONLExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}
	if options.ElasticsearchExporter != nil {
		options.ElasticsearchExporter.HttpClient = options.HttpClient
		exporter, err := es.New(options.ElasticsearchExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}
	if options.SplunkExporter != nil {
		options.SplunkExporter.HttpClient = options.HttpClient
		exporter, err := splunk.New(options.SplunkExporter)
		if err != nil {
			return nil, errorutil.NewWithErr(err).Wrap(ErrExportClientCreation)
		}
		client.exporters = append(client.exporters, exporter)
	}

	storage, err := dedupe.New(db)
	if err != nil {
		return nil, err
	}
	client.dedupe = storage
	return client, nil
}

// CreateConfigIfNotExists creates report-config if it doesn't exists
func CreateConfigIfNotExists() error {
	reportingConfig := config.DefaultConfig.GetReportingConfigFilePath()

	if fileutil.FileExists(reportingConfig) {
		return nil
	}
	values := stringslice.StringSlice{Value: []string{}}

	options := &Options{
		AllowList:             &filters.Filter{Tags: values},
		DenyList:              &filters.Filter{Tags: values},
		GitHub:                &github.Options{},
		GitLab:                &gitlab.Options{},
		Gitea:                 &gitea.Options{},
		Jira:                  &jira.Options{},
		MarkdownExporter:      &markdown.Options{},
		SarifExporter:         &sarif.Options{},
		ElasticsearchExporter: &es.Options{},
		SplunkExporter:        &splunk.Options{},
		JSONExporter:          &json_exporter.Options{},
		JSONLExporter:         &jsonl.Options{},
	}
	reportingFile, err := os.Create(reportingConfig)
	if err != nil {
		return errorutil.NewWithErr(err).Msgf("could not create config file")
	}
	defer reportingFile.Close()

	err = yaml.NewEncoder(reportingFile).Encode(options)
	return err
}

// RegisterTracker registers a custom tracker to the reporter
func (c *ReportingClient) RegisterTracker(tracker Tracker) {
	c.trackers = append(c.trackers, tracker)
}

// RegisterExporter registers a custom exporter to the reporter
func (c *ReportingClient) RegisterExporter(exporter Exporter) {
	c.exporters = append(c.exporters, exporter)
}

// Close closes the issue tracker reporting client
func (c *ReportingClient) Close() {
	c.dedupe.Close()
	for _, exporter := range c.exporters {
		exporter.Close()
	}
}

// CreateIssue creates an issue in the tracker
func (c *ReportingClient) CreateIssue(event *output.ResultEvent) error {
	// process global allow/deny list
	if c.options.AllowList != nil && !c.options.AllowList.GetMatch(event) {
		return nil
	}
	if c.options.DenyList != nil && c.options.DenyList.GetMatch(event) {
		return nil
	}

	unique, err := c.dedupe.Index(event)
	if unique {
		for _, tracker := range c.trackers {
			// process tracker specific allow/deny list
			if tracker.ShouldFilter(event) {
				continue
			}
			if trackerErr := tracker.CreateIssue(event); trackerErr != nil {
				err = multierr.Append(err, trackerErr)
			}
		}
		for _, exporter := range c.exporters {
			if exportErr := exporter.Export(event); exportErr != nil {
				err = multierr.Append(err, exportErr)
			}
		}
	}
	return err
}

func (c *ReportingClient) GetReportingOptions() *Options {
	return c.options
}

func (c *ReportingClient) Clear() {
	c.dedupe.Clear()
}
