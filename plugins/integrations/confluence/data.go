package confluence

import (
	coredata "github.com/fluxplane/engine/core/data"
	runtimedata "github.com/fluxplane/engine/runtime/data"
)

const (
	ConfluencePageView  coredata.ViewName = "confluence.page"
	ConfluenceSpaceView coredata.ViewName = "confluence.space"
)

// DataSourceSpec describes the Confluence source schema and default materialized views.
func DataSourceSpec() coredata.SourceSpec {
	return runtimedata.SourceFromDatasource("confluence", Name, entitySpecs(), DataViews()...)
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
