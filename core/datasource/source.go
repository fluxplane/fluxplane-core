package datasource

import (
	"strings"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
)

// SpecFromDataSource derives a catalog datasource spec from a plugin-owned
// data source schema.
func SpecFromDataSource(source coredata.SourceSpec) Spec {
	spec := Spec{
		Name:        Name(strings.TrimSpace(string(source.Name))),
		Description: strings.TrimSpace(source.Description),
		Kind:        strings.TrimSpace(source.Kind),
		Config:      cloneStringMap(source.Config),
		Annotations: cloneStringMap(source.Annotations),
	}
	if len(source.Entities) > 0 {
		spec.Entities = make([]EntityType, 0, len(source.Entities))
		for _, entity := range source.Entities {
			if typ := strings.TrimSpace(string(entity.Type)); typ != "" {
				spec.Entities = append(spec.Entities, EntityType(typ))
			}
		}
	}
	return spec
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
