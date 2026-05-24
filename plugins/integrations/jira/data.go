package jira

import (
	coredata "github.com/fluxplane/engine/core/data"
	runtimedata "github.com/fluxplane/engine/runtime/data"
	operationruntime "github.com/fluxplane/engine/runtime/operation"
)

const (
	JiraIssueView   coredata.ViewName = "jira.issue"
	JiraProjectView coredata.ViewName = "jira.project"
)

// DataSourceSpec describes the Jira source schema and default materialized views.
func DataSourceSpec() coredata.SourceSpec {
	spec := runtimedata.SourceFromDatasource("jira", Name, entitySpecs(), DataViews()...)
	spec.ConfigSchema = operationruntime.SchemaFor[datasourceConfig]()
	return spec
}

type datasourceConfig struct {
	Instance string `json:"instance,omitempty" jsonschema:"description=Jira plugin instance that provides credentials for this datasource."`
}

// DataViews returns the Jira materializations the query API should prefer.
func DataViews() []coredata.ViewSpec {
	return []coredata.ViewSpec{
		runtimedata.ViewOf[Issue](
			JiraIssueView,
			coredata.EntityType(IssueEntity),
			runtimedata.WithViewDescription("Jira issues by key, summary, description, status, and project."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
		runtimedata.ViewOf[Project](
			JiraProjectView,
			coredata.EntityType(ProjectEntity),
			runtimedata.WithViewDescription("Jira projects by key, name, and project type."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
	}
}
