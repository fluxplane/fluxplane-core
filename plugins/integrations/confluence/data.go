package confluence

import (
	coredata "github.com/fluxplane/fluxplane-core/core/data"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	operationruntime "github.com/fluxplane/fluxplane-core/runtime/operation"
)

const (
	ConfluencePageView  coredata.ViewName = "confluence.page"
	ConfluenceSpaceView coredata.ViewName = "confluence.space"
)

// DataSourceSpec describes the Confluence source schema and default materialized views.
func DataSourceSpec() coredata.SourceSpec {
	spec := runtimedata.SourceFromDatasource("confluence", Name, entitySpecs(), DataViews()...)
	spec.ConfigSchema = operationruntime.SchemaFor[datasourceConfig]()
	return spec
}

type datasourceConfig struct {
	Instance string `json:"instance,omitempty" jsonschema:"description=Confluence plugin instance that provides credentials for this datasource."`
}

// DataViews returns the Confluence materializations the query API should prefer.
func DataViews() []coredata.ViewSpec {
	return []coredata.ViewSpec{
		runtimedata.ViewOf[Page](
			ConfluencePageView,
			coredata.EntityType(PageEntity),
			runtimedata.WithViewDescription("Confluence pages by id, title, space, status, URL, and body."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
		runtimedata.ViewOf[Space](
			ConfluenceSpaceView,
			coredata.EntityType(SpaceEntity),
			runtimedata.WithViewDescription("Confluence spaces by id, key, name, type, status, and description."),
			runtimedata.WithViewQueryHints(coredata.QueryGet, coredata.QueryList, coredata.QuerySearch),
		),
	}
}
