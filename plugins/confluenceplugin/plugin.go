package confluenceplugin

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/agentruntime/core/datasource"
	"github.com/fluxplane/agentruntime/core/resource"
	coresecret "github.com/fluxplane/agentruntime/core/secret"
	"github.com/fluxplane/agentruntime/orchestration/pluginhost"
	"github.com/fluxplane/agentruntime/plugins/atlassianplugin"
	runtimedatasource "github.com/fluxplane/agentruntime/runtime/datasource"
	runtimesecret "github.com/fluxplane/agentruntime/runtime/secret"
	"github.com/fluxplane/agentruntime/runtime/system"
)

const Name = "confluence"

const defaultPageSize = 50

var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

type Plugin struct {
	pluginhost.Configurable[atlassianplugin.Config]
	system system.System
	store  runtimesecret.FileStore
	ref    resource.PluginRef
	cfg    atlassianplugin.Config
}

var _ pluginhost.Plugin = Plugin{}
var _ pluginhost.InstanceFactory = Plugin{}
var _ pluginhost.DatasourceProviderContributor = Plugin{}
var _ pluginhost.AuthMethodContributor = Plugin{}

func New(sys system.System, stores ...runtimesecret.FileStore) Plugin {
	store := runtimesecret.NewFileStore(atlassianplugin.DefaultAuthStorePath)
	if len(stores) > 0 {
		store = stores[0]
	}
	return Plugin{system: sys, store: store}
}

func (Plugin) Manifest() pluginhost.Manifest {
	return pluginhost.Manifest{Name: Name, Description: "Confluence datasource access."}
}

func (p Plugin) Instantiate(_ context.Context, ctx pluginhost.Context) (pluginhost.Plugin, error) {
	cfg, err := pluginhost.ConfigAs[atlassianplugin.Config](ctx)
	if err != nil {
		return nil, err
	}
	return Plugin{system: p.system, store: p.store, ref: ctx.Ref, cfg: atlassianplugin.NormalizeConfig(cfg)}, nil
}

func (p Plugin) Contributions(_ context.Context, _ pluginhost.Context) (resource.ContributionBundle, error) {
	return resource.ContributionBundle{}, nil
}

func (p Plugin) DatasourceProviders(_ context.Context, ctx pluginhost.Context) ([]coredatasource.Provider, error) {
	p = p.withRef(ctx.Ref)
	return []coredatasource.Provider{confluenceDatasourceProvider{plugin: p}}, nil
}

func (p Plugin) AuthMethods(_ context.Context, ctx pluginhost.Context) ([]coresecret.AuthMethodSpec, error) {
	p = p.withRef(ctx.Ref)
	return atlassianplugin.AuthMethods(Name, p.ref, AtlassianProduct(), p.cfg), nil
}

func AtlassianProduct() atlassianplugin.Product {
	return atlassianplugin.Product{
		Name:         Name,
		DisplayName:  "Confluence Cloud",
		ResourcePath: "confluence",
		RESTPath:     "/wiki/api/v2",
		Scopes:       []string{"read:page:confluence", "read:space:confluence", "offline_access"},
	}
}

func (p Plugin) withRef(ref resource.PluginRef) Plugin {
	if p.ref.Name == "" && ref.Name != "" {
		p.ref = ref
	}
	if p.ref.Name == "" {
		p.ref.Name = Name
	}
	return p
}

func (p Plugin) session(ctx context.Context) (atlassianplugin.Session, error) {
	return atlassianplugin.Resolve(ctx, p.system, p.store, Name, p.ref, AtlassianProduct(), p.cfg)
}

const PageEntity coredatasource.EntityType = "confluence.page"
const SpaceEntity coredatasource.EntityType = "confluence.space"

type Page struct {
	ID       string `json:"id" datasource:"id,filterable" jsonschema:"description=Confluence page id.,required"`
	Title    string `json:"title,omitempty" datasource:"searchable" jsonschema:"description=Page title."`
	SpaceID  string `json:"space_id,omitempty" datasource:"filterable" jsonschema:"description=Confluence space id."`
	ParentID string `json:"parent_id,omitempty" datasource:"filterable" jsonschema:"description=Parent page id."`
	Status   string `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Page status."`
	Version  int    `json:"version,omitempty" jsonschema:"description=Page version number."`
	URL      string `json:"url,omitempty" datasource:"url" jsonschema:"description=Human-facing Confluence URL."`
	Body     string `json:"body,omitempty" datasource:"searchable" corpus:"body" jsonschema:"description=Plain-text page body."`
}

type Space struct {
	ID          string `json:"id" datasource:"id,filterable" jsonschema:"description=Confluence space id.,required"`
	Key         string `json:"key,omitempty" datasource:"searchable,filterable" jsonschema:"description=Confluence space key."`
	Name        string `json:"name,omitempty" datasource:"searchable" jsonschema:"description=Space name."`
	Type        string `json:"type,omitempty" datasource:"filterable" jsonschema:"description=Space type."`
	Status      string `json:"status,omitempty" datasource:"filterable" jsonschema:"description=Space status."`
	Description string `json:"description,omitempty" datasource:"searchable" corpus:"body" jsonschema:"description=Plain-text space description."`
	URL         string `json:"url,omitempty" datasource:"url" jsonschema:"description=Human-facing Confluence URL."`
}

type confluenceDatasourceProvider struct {
	plugin Plugin
}

func (p confluenceDatasourceProvider) Entities() []coredatasource.EntitySpec {
	return entitySpecs()
}

func (p confluenceDatasourceProvider) Open(_ context.Context, spec coredatasource.Spec) (coredatasource.Accessor, error) {
	if strings.TrimSpace(spec.Kind) != Name {
		return nil, fmt.Errorf("unsupported datasource kind %q", spec.Kind)
	}
	var selected []coredatasource.EntitySpec
	if len(spec.Entities) == 0 {
		selected = entitySpecs()
	} else {
		var err error
		selected, err = runtimedatasource.SelectEntities(Name, entitySpecs(), spec.Entities)
		if err != nil {
			return nil, err
		}
	}
	instance := strings.TrimSpace(spec.Config["instance"])
	if instance != "" && instance != p.plugin.ref.InstanceName() {
		return nil, fmt.Errorf("confluence datasource instance %q does not match plugin instance %q", instance, p.plugin.ref.InstanceName())
	}
	return confluenceAccessor{spec: spec, plugin: p.plugin, entities: selected}, nil
}

type confluenceAccessor struct {
	spec     coredatasource.Spec
	plugin   Plugin
	entities []coredatasource.EntitySpec
}

func (a confluenceAccessor) Spec() coredatasource.Spec { return a.spec }

func (a confluenceAccessor) Entities() []coredatasource.EntitySpec {
	return append([]coredatasource.EntitySpec(nil), a.entities...)
}

func (a confluenceAccessor) Search(ctx context.Context, req coredatasource.SearchRequest) (coredatasource.SearchResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = PageEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.SearchResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	scanLimit := max(limit, 100)
	switch entity {
	case PageEntity:
		pages, _, err := confluenceListPages(ctx, a.plugin.system, session, "", scanLimit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(filterPages(pages, req.Query), a.pageRecord)
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
	case SpaceEntity:
		spaces, _, err := confluenceListSpaces(ctx, a.plugin.system, session, "", scanLimit)
		if err != nil {
			return coredatasource.SearchResult{}, err
		}
		records := runtimedatasource.NonEmptyRecordsFrom(filterSpaces(spaces, req.Query), a.spaceRecord)
		if len(records) > limit {
			records = records[:limit]
		}
		return runtimedatasource.SearchResult(a.spec.Name, entity, records, len(records)), nil
	default:
		return coredatasource.SearchResult{}, fmt.Errorf("datasource %q entity %q does not support search", a.spec.Name, entity)
	}
}

func (a confluenceAccessor) List(ctx context.Context, req coredatasource.ListRequest) (coredatasource.ListResult, error) {
	entity := req.Entity
	if entity == "" {
		entity = PageEntity
	}
	if !runtimedatasource.HasEntity(a.entities, entity) {
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.ListResult{}, err
	}
	limit := normalizedLimit(req.Limit)
	switch entity {
	case PageEntity:
		pages, next, err := confluenceListPages(ctx, a.plugin.system, session, req.Cursor, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(pages, a.pageRecord), -1, next), nil
	case SpaceEntity:
		spaces, next, err := confluenceListSpaces(ctx, a.plugin.system, session, req.Cursor, limit)
		if err != nil {
			return coredatasource.ListResult{}, err
		}
		return runtimedatasource.ListResult(a.spec.Name, entity, runtimedatasource.NonEmptyRecordsFrom(spaces, a.spaceRecord), -1, next), nil
	default:
		return coredatasource.ListResult{}, fmt.Errorf("datasource %q entity %q does not support list", a.spec.Name, entity)
	}
}

func (a confluenceAccessor) Get(ctx context.Context, req coredatasource.GetRequest) (coredatasource.Record, error) {
	if !runtimedatasource.HasEntity(a.entities, req.Entity) {
		return coredatasource.Record{}, fmt.Errorf("datasource %q does not expose entity %q", a.spec.Name, req.Entity)
	}
	session, err := a.plugin.session(ctx)
	if err != nil {
		return coredatasource.Record{}, err
	}
	switch req.Entity {
	case PageEntity:
		page, err := confluenceGetPage(ctx, a.plugin.system, session, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.pageRecord(page), nil
	case SpaceEntity:
		space, err := confluenceGetSpace(ctx, a.plugin.system, session, req.ID)
		if err != nil {
			return coredatasource.Record{}, err
		}
		return a.spaceRecord(space), nil
	default:
		return coredatasource.Record{}, fmt.Errorf("datasource %q entity %q does not support get", a.spec.Name, req.Entity)
	}
}

func (a confluenceAccessor) BatchGet(ctx context.Context, req coredatasource.BatchGetRequest) (coredatasource.BatchGetResult, error) {
	out := coredatasource.BatchGetResult{Datasource: a.spec.Name, Entity: req.Entity}
	for _, id := range req.IDs {
		record, err := a.Get(ctx, coredatasource.GetRequest{Entity: req.Entity, ID: id})
		if err != nil {
			out.Errors = append(out.Errors, coredatasource.BatchGetError{ID: id, Message: err.Error()})
			continue
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func (a confluenceAccessor) Corpus(ctx context.Context, req coredatasource.CorpusRequest) (coredatasource.CorpusPage, error) {
	entity := req.Entity
	if entity == "" {
		entity = PageEntity
	}
	result, err := a.List(ctx, coredatasource.ListRequest{Entity: entity, Cursor: req.Cursor, Limit: req.Limit})
	if err != nil {
		return coredatasource.CorpusPage{}, err
	}
	return coredatasource.CorpusPage{
		Documents:  runtimedatasource.RecordsToCorpusDocuments(result.Records),
		NextCursor: result.NextCursor,
		Complete:   result.Complete,
	}, nil
}

func (a confluenceAccessor) pageRecord(page Page) coredatasource.Record {
	return coredatasource.Record{
		ID:         page.ID,
		Datasource: a.spec.Name,
		Entity:     PageEntity,
		Title:      firstNonEmpty(page.Title, page.ID),
		Content:    page.Body,
		URL:        page.URL,
		Metadata: map[string]string{
			"space_id":  page.SpaceID,
			"parent_id": page.ParentID,
			"status":    page.Status,
			"version":   strconv.Itoa(page.Version),
		},
		Raw: page,
	}
}

func (a confluenceAccessor) spaceRecord(space Space) coredatasource.Record {
	return coredatasource.Record{
		ID:         space.ID,
		Datasource: a.spec.Name,
		Entity:     SpaceEntity,
		Title:      firstNonEmpty(space.Name, space.Key, space.ID),
		Content:    space.Description,
		URL:        space.URL,
		Metadata: map[string]string{
			"key":    space.Key,
			"type":   space.Type,
			"status": space.Status,
		},
		Raw: space,
	}
}

func entitySpecs() []coredatasource.EntitySpec {
	page := runtimedatasource.EntityOf[Page](PageEntity, "Confluence page.")
	page.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityIndex,
	}
	page.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "confluence_page_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/wiki/(?:spaces/[^/\s<>"']+/pages|pages)/(\d+)`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.95,
		},
	}
	space := runtimedatasource.EntityOf[Space](SpaceEntity, "Confluence space.")
	space.Capabilities = []coredatasource.EntityCapability{
		coredatasource.EntityCapabilitySearch,
		coredatasource.EntityCapabilityList,
		coredatasource.EntityCapabilityGet,
		coredatasource.EntityCapabilityIndex,
	}
	space.Detectors = []coredatasource.DetectorSpec{
		{
			Name:          "confluence_space_url",
			Kind:          coredatasource.DetectorURL,
			Pattern:       `https?://[^\s<>"']+/wiki/spaces/([^/\s<>"']+)`,
			IDTemplate:    "$1",
			QueryTemplate: "$1",
			URLTemplate:   "$0",
			Confidence:    0.9,
		},
	}
	return []coredatasource.EntitySpec{page, space}
}

type confluenceListResponse[T any] struct {
	Results []T              `json:"results"`
	Links   confluenceLinks  `json:"_links"`
	Meta    confluenceCursor `json:"meta"`
}

type confluenceCursor struct {
	HasMoreCursor string `json:"hasMoreCursor"`
}

type confluenceLinks struct {
	Next  string `json:"next"`
	WebUI string `json:"webui"`
}

type confluencePage struct {
	ID       string             `json:"id"`
	Status   string             `json:"status"`
	Title    string             `json:"title"`
	SpaceID  string             `json:"spaceId"`
	ParentID string             `json:"parentId"`
	Body     confluencePageBody `json:"body"`
	Version  confluenceVersion  `json:"version"`
	Links    confluenceLinks    `json:"_links"`
}

type confluenceV1Page struct {
	ID        string               `json:"id"`
	Status    string               `json:"status"`
	Title     string               `json:"title"`
	Space     confluenceV1SpaceRef `json:"space"`
	Ancestors []confluenceV1Page   `json:"ancestors"`
	Body      confluencePageBody   `json:"body"`
	Version   confluenceVersion    `json:"version"`
	Links     confluenceLinks      `json:"_links"`
}

type confluenceV1SpaceRef struct {
	ID    int64           `json:"id"`
	Key   string          `json:"key"`
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Links confluenceLinks `json:"_links"`
}

type confluencePageBody struct {
	Storage confluenceBodyValue `json:"storage"`
}

type confluenceBodyValue struct {
	Value string `json:"value"`
}

type confluenceVersion struct {
	Number int `json:"number"`
}

type confluenceSpace struct {
	ID          string                `json:"id"`
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Type        string                `json:"type"`
	Status      string                `json:"status"`
	Description confluenceDescription `json:"description"`
	Links       confluenceLinks       `json:"_links"`
}

type confluenceV1Space struct {
	ID          int64                 `json:"id"`
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Type        string                `json:"type"`
	Status      string                `json:"status"`
	Description confluenceDescription `json:"description"`
	Links       confluenceLinks       `json:"_links"`
}

type confluenceDescription struct {
	Plain confluenceBodyValue `json:"plain"`
	View  confluenceBodyValue `json:"view"`
}

func confluenceListPages(ctx context.Context, sys system.System, session atlassianplugin.Session, cursor string, limit int) ([]Page, string, error) {
	if confluenceUseV1(session) {
		return confluenceListPagesV1(ctx, sys, session, cursor, limit)
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(normalizedLimit(limit)))
	query.Set("body-format", "storage")
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("cursor", cursor)
	}
	var data confluenceListResponse[confluencePage]
	if _, err := atlassianplugin.DoJSON(ctx, sys, session, http.MethodGet, "/pages?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	pages := make([]Page, 0, len(data.Results))
	for _, page := range data.Results {
		pages = append(pages, pageFromConfluence(session.SiteURL, page))
	}
	return pages, nextCursor(data.Links.Next), nil
}

func confluenceListSpaces(ctx context.Context, sys system.System, session atlassianplugin.Session, cursor string, limit int) ([]Space, string, error) {
	if confluenceUseV1(session) {
		return confluenceListSpacesV1(ctx, sys, session, cursor, limit)
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(normalizedLimit(limit)))
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("cursor", cursor)
	}
	var data confluenceListResponse[confluenceSpace]
	if _, err := atlassianplugin.DoJSON(ctx, sys, session, http.MethodGet, "/spaces?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	spaces := make([]Space, 0, len(data.Results))
	for _, space := range data.Results {
		spaces = append(spaces, spaceFromConfluence(session.SiteURL, space))
	}
	return spaces, nextCursor(data.Links.Next), nil
}

func confluenceGetPage(ctx context.Context, sys system.System, session atlassianplugin.Session, id string) (Page, error) {
	if confluenceUseV1(session) {
		return confluenceGetPageV1(ctx, sys, session, id)
	}
	var raw confluencePage
	if _, err := atlassianplugin.DoJSON(ctx, sys, session, http.MethodGet, "/pages/"+url.PathEscape(strings.TrimSpace(id))+"?body-format=storage", nil, &raw); err != nil {
		return Page{}, err
	}
	return pageFromConfluence(session.SiteURL, raw), nil
}

func confluenceGetSpace(ctx context.Context, sys system.System, session atlassianplugin.Session, id string) (Space, error) {
	if confluenceUseV1(session) {
		return confluenceGetSpaceV1(ctx, sys, session, id)
	}
	if !looksNumeric(id) {
		spaces, _, err := confluenceListSpacesByKey(ctx, sys, session, id)
		if err != nil {
			return Space{}, err
		}
		if len(spaces) == 0 {
			return Space{}, fmt.Errorf("confluence space %q not found", id)
		}
		return spaces[0], nil
	}
	var raw confluenceSpace
	if _, err := atlassianplugin.DoJSON(ctx, sys, session, http.MethodGet, "/spaces/"+url.PathEscape(strings.TrimSpace(id)), nil, &raw); err != nil {
		return Space{}, err
	}
	return spaceFromConfluence(session.SiteURL, raw), nil
}

func confluenceListSpacesByKey(ctx context.Context, sys system.System, session atlassianplugin.Session, key string) ([]Space, string, error) {
	if confluenceUseV1(session) {
		return confluenceListSpacesByKeyV1(ctx, sys, session, key)
	}
	query := url.Values{}
	query.Set("limit", "1")
	query.Set("keys", strings.TrimSpace(key))
	var data confluenceListResponse[confluenceSpace]
	if _, err := atlassianplugin.DoJSON(ctx, sys, session, http.MethodGet, "/spaces?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	spaces := make([]Space, 0, len(data.Results))
	for _, space := range data.Results {
		spaces = append(spaces, spaceFromConfluence(session.SiteURL, space))
	}
	return spaces, nextCursor(data.Links.Next), nil
}

func confluenceListPagesV1(ctx context.Context, sys system.System, session atlassianplugin.Session, cursor string, limit int) ([]Page, string, error) {
	query := url.Values{}
	query.Set("type", "page")
	query.Set("limit", strconv.Itoa(normalizedLimit(limit)))
	query.Set("expand", "body.storage,version,space,ancestors")
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("start", cursor)
	}
	var data confluenceListResponse[confluenceV1Page]
	v1 := confluenceV1Session(session)
	if _, err := atlassianplugin.DoJSON(ctx, sys, v1, http.MethodGet, "/content?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	pages := make([]Page, 0, len(data.Results))
	for _, page := range data.Results {
		pages = append(pages, pageFromConfluenceV1(session.SiteURL, page))
	}
	return pages, nextCursor(data.Links.Next), nil
}

func confluenceListSpacesV1(ctx context.Context, sys system.System, session atlassianplugin.Session, cursor string, limit int) ([]Space, string, error) {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(normalizedLimit(limit)))
	query.Set("expand", "description.plain")
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		query.Set("start", cursor)
	}
	var data confluenceListResponse[confluenceV1Space]
	v1 := confluenceV1Session(session)
	if _, err := atlassianplugin.DoJSON(ctx, sys, v1, http.MethodGet, "/space?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	spaces := make([]Space, 0, len(data.Results))
	for _, space := range data.Results {
		spaces = append(spaces, spaceFromConfluenceV1(session.SiteURL, space))
	}
	return spaces, nextCursor(data.Links.Next), nil
}

func confluenceGetPageV1(ctx context.Context, sys system.System, session atlassianplugin.Session, id string) (Page, error) {
	var raw confluenceV1Page
	v1 := confluenceV1Session(session)
	if _, err := atlassianplugin.DoJSON(ctx, sys, v1, http.MethodGet, "/content/"+url.PathEscape(strings.TrimSpace(id))+"?expand=body.storage,version,space,ancestors", nil, &raw); err != nil {
		return Page{}, err
	}
	return pageFromConfluenceV1(session.SiteURL, raw), nil
}

func confluenceGetSpaceV1(ctx context.Context, sys system.System, session atlassianplugin.Session, id string) (Space, error) {
	spaces, _, err := confluenceListSpacesByKeyV1(ctx, sys, session, id)
	if err != nil {
		return Space{}, err
	}
	if len(spaces) == 0 {
		return Space{}, fmt.Errorf("confluence space %q not found", id)
	}
	return spaces[0], nil
}

func confluenceListSpacesByKeyV1(ctx context.Context, sys system.System, session atlassianplugin.Session, key string) ([]Space, string, error) {
	query := url.Values{}
	query.Set("limit", "1")
	query.Set("expand", "description.plain")
	query.Set("spaceKey", strings.TrimSpace(key))
	var data confluenceListResponse[confluenceV1Space]
	v1 := confluenceV1Session(session)
	if _, err := atlassianplugin.DoJSON(ctx, sys, v1, http.MethodGet, "/space?"+query.Encode(), nil, &data); err != nil {
		return nil, "", err
	}
	spaces := make([]Space, 0, len(data.Results))
	for _, space := range data.Results {
		spaces = append(spaces, spaceFromConfluenceV1(session.SiteURL, space))
	}
	return spaces, nextCursor(data.Links.Next), nil
}

func pageFromConfluence(siteURL string, page confluencePage) Page {
	return Page{
		ID:       page.ID,
		Title:    page.Title,
		SpaceID:  page.SpaceID,
		ParentID: page.ParentID,
		Status:   page.Status,
		Version:  page.Version.Number,
		URL:      confluenceURL(siteURL, page.Links.WebUI),
		Body:     plainText(page.Body.Storage.Value),
	}
}

func pageFromConfluenceV1(siteURL string, page confluenceV1Page) Page {
	parentID := ""
	if len(page.Ancestors) > 0 {
		parentID = page.Ancestors[len(page.Ancestors)-1].ID
	}
	spaceID := ""
	if page.Space.ID != 0 {
		spaceID = strconv.FormatInt(page.Space.ID, 10)
	}
	return Page{
		ID:       page.ID,
		Title:    page.Title,
		SpaceID:  spaceID,
		ParentID: parentID,
		Status:   page.Status,
		Version:  page.Version.Number,
		URL:      confluenceURL(siteURL, page.Links.WebUI),
		Body:     plainText(page.Body.Storage.Value),
	}
}

func spaceFromConfluence(siteURL string, space confluenceSpace) Space {
	description := plainText(firstNonEmpty(space.Description.Plain.Value, space.Description.View.Value))
	return Space{
		ID:          space.ID,
		Key:         space.Key,
		Name:        space.Name,
		Type:        space.Type,
		Status:      space.Status,
		Description: description,
		URL:         confluenceURL(siteURL, space.Links.WebUI),
	}
}

func spaceFromConfluenceV1(siteURL string, space confluenceV1Space) Space {
	id := strings.TrimSpace(space.Key)
	if id == "" && space.ID != 0 {
		id = strconv.FormatInt(space.ID, 10)
	}
	description := plainText(firstNonEmpty(space.Description.Plain.Value, space.Description.View.Value))
	return Space{
		ID:          id,
		Key:         space.Key,
		Name:        space.Name,
		Type:        space.Type,
		Status:      space.Status,
		Description: description,
		URL:         confluenceURL(siteURL, space.Links.WebUI),
	}
}

func confluenceUseV1(session atlassianplugin.Session) bool {
	return session.Method == atlassianplugin.TokenMethod
}

func confluenceV1Session(session atlassianplugin.Session) atlassianplugin.Session {
	base := strings.TrimRight(session.BaseURL, "/")
	if strings.HasSuffix(base, "/wiki/api/v2") {
		session.BaseURL = strings.TrimSuffix(base, "/wiki/api/v2") + "/wiki/rest/api"
	}
	return session
}

func confluenceURL(siteURL, webUI string) string {
	webUI = strings.TrimSpace(webUI)
	if strings.HasPrefix(webUI, "http://") || strings.HasPrefix(webUI, "https://") {
		return webUI
	}
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if siteURL == "" || webUI == "" {
		return webUI
	}
	return siteURL + "/" + strings.TrimLeft(webUI, "/")
}

func plainText(value string) string {
	value = html.UnescapeString(value)
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func filterPages(pages []Page, query string) []Page {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return pages
	}
	var out []Page
	for _, page := range pages {
		values := []string{page.ID, page.Title, page.SpaceID, page.ParentID, page.Status, page.Body}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				out = append(out, page)
				break
			}
		}
	}
	return out
}

func filterSpaces(spaces []Space, query string) []Space {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return spaces
	}
	var out []Space
	for _, space := range spaces {
		values := []string{space.ID, space.Key, space.Name, space.Type, space.Status, space.Description}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), query) {
				out = append(out, space)
				break
			}
		}
	}
	return out
}

func nextCursor(next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return ""
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return ""
	}
	if cursor := parsed.Query().Get("cursor"); cursor != "" {
		return cursor
	}
	return parsed.Query().Get("start")
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func looksNumeric(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
