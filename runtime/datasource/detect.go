package datasource

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	coredatasource "github.com/fluxplane/fluxplane-datasource"
)

const (
	defaultMaxRefs        = 20
	defaultMaxSourceBytes = 12000
)

// DetectOptions bounds local datasource reference detection.
type DetectOptions struct {
	MaxRefs        int
	MaxSourceBytes int
}

// Detect applies provider-neutral entity detector specs to local text and
// metadata. It never opens datasources or performs IO.
func Detect(ctx context.Context, input coredatasource.DetectionInput, accessors []coredatasource.Accessor, opts DetectOptions) []coredatasource.RecordRef {
	if ctx == nil {
		ctx = context.Background()
	}
	maxRefs := opts.MaxRefs
	if input.MaxRefs > 0 && (maxRefs <= 0 || input.MaxRefs < maxRefs) {
		maxRefs = input.MaxRefs
	}
	if maxRefs <= 0 {
		maxRefs = defaultMaxRefs
	}
	maxSourceBytes := opts.MaxSourceBytes
	if maxSourceBytes <= 0 {
		maxSourceBytes = defaultMaxSourceBytes
	}
	var refs []coredatasource.RecordRef
	seen := map[string]bool{}
	for _, accessor := range sortedAccessors(accessors) {
		if accessor == nil {
			continue
		}
		spec := accessor.Spec()
		for _, entity := range sortedEntities(accessor.Entities()) {
			for _, detector := range sortedDetectors(entity.Detectors) {
				if ctx.Err() != nil || len(refs) >= maxRefs {
					return sortedRefs(refs)
				}
				refs = appendDetected(refs, seen, detectEntity(spec.Name, entity.Type, detector, input.Sources, maxSourceBytes, maxRefs-len(refs))...)
				if len(refs) >= maxRefs {
					return sortedRefs(refs[:maxRefs])
				}
			}
		}
	}
	return sortedRefs(refs)
}

func sortedAccessors(accessors []coredatasource.Accessor) []coredatasource.Accessor {
	out := append([]coredatasource.Accessor(nil), accessors...)
	sort.SliceStable(out, func(i, j int) bool {
		var left, right string
		if out[i] != nil {
			left = string(out[i].Spec().Name)
		}
		if out[j] != nil {
			right = string(out[j].Spec().Name)
		}
		return left < right
	})
	return out
}

func sortedEntities(entities []coredatasource.EntitySpec) []coredatasource.EntitySpec {
	out := append([]coredatasource.EntitySpec(nil), entities...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

func sortedDetectors(detectors []coredatasource.DetectorSpec) []coredatasource.DetectorSpec {
	out := append([]coredatasource.DetectorSpec(nil), detectors...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedRefs(refs []coredatasource.RecordRef) []coredatasource.RecordRef {
	out := append([]coredatasource.RecordRef(nil), refs...)
	sort.SliceStable(out, func(i, j int) bool {
		return refSortKey(out[i]) < refSortKey(out[j])
	})
	return out
}

func detectEntity(datasource coredatasource.Name, entity coredatasource.EntityType, detector coredatasource.DetectorSpec, sources []coredatasource.DetectionSource, maxSourceBytes int, limit int) []coredatasource.RecordRef {
	if limit <= 0 || strings.TrimSpace(detector.Pattern) == "" {
		return nil
	}
	switch detector.Kind {
	case coredatasource.DetectorRegex, coredatasource.DetectorURL:
		return detectRegex(datasource, entity, detector, sources, maxSourceBytes, limit)
	case coredatasource.DetectorStructured:
		return detectStructured(datasource, entity, detector, sources, limit)
	default:
		return nil
	}
}

func detectRegex(datasource coredatasource.Name, entity coredatasource.EntityType, detector coredatasource.DetectorSpec, sources []coredatasource.DetectionSource, maxSourceBytes int, limit int) []coredatasource.RecordRef {
	re, err := regexp.Compile(detector.Pattern)
	if err != nil {
		return nil
	}
	var refs []coredatasource.RecordRef
	for _, source := range sources {
		text := truncateBytes(source.Text, maxSourceBytes)
		if text == "" {
			continue
		}
		for _, match := range re.FindAllStringSubmatchIndex(text, -1) {
			if len(refs) >= limit {
				return refs
			}
			sourceText := text[match[0]:match[1]]
			ref := coredatasource.RecordRef{
				Datasource:  datasource,
				Entity:      entity,
				Confidence:  confidence(detector.Confidence),
				SourceText:  sourceText,
				SourceKind:  source.Kind,
				Detector:    detector.Name,
				Annotations: cloneStringMap(detector.Annotations),
			}
			if detector.IDTemplate != "" {
				ref.ID = expandTemplate(re, text, match, detector.IDTemplate)
			}
			if detector.QueryTemplate != "" {
				ref.Query = expandTemplate(re, text, match, detector.QueryTemplate)
			}
			if detector.URLTemplate != "" {
				ref.URL = expandTemplate(re, text, match, detector.URLTemplate)
			}
			if ref.URL == "" && detector.Kind == coredatasource.DetectorURL {
				ref.URL = sourceText
			}
			if ref.ID == "" && ref.Query == "" && ref.URL == "" {
				ref.Query = sourceText
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

func detectStructured(datasource coredatasource.Name, entity coredatasource.EntityType, detector coredatasource.DetectorSpec, sources []coredatasource.DetectionSource, limit int) []coredatasource.RecordRef {
	var refs []coredatasource.RecordRef
	key := strings.TrimSpace(detector.Pattern)
	for _, source := range sources {
		if len(refs) >= limit {
			return refs
		}
		if source.Metadata == nil {
			continue
		}
		value := strings.TrimSpace(source.Metadata[key])
		if value == "" {
			continue
		}
		ref := coredatasource.RecordRef{
			Datasource:  datasource,
			Entity:      entity,
			Confidence:  confidence(detector.Confidence),
			SourceText:  value,
			SourceKind:  source.Kind,
			Detector:    detector.Name,
			Annotations: cloneStringMap(detector.Annotations),
		}
		if detector.IDTemplate != "" {
			ref.ID = strings.ReplaceAll(detector.IDTemplate, "$0", value)
		}
		if detector.QueryTemplate != "" {
			ref.Query = strings.ReplaceAll(detector.QueryTemplate, "$0", value)
		}
		if detector.URLTemplate != "" {
			ref.URL = strings.ReplaceAll(detector.URLTemplate, "$0", value)
		}
		if ref.ID == "" && ref.Query == "" && ref.URL == "" {
			ref.Query = value
		}
		refs = append(refs, ref)
	}
	return refs
}

func expandTemplate(re *regexp.Regexp, text string, match []int, tmpl string) string {
	var out []byte
	out = re.ExpandString(out, tmpl, text, match)
	return strings.TrimSpace(string(out))
}

func appendDetected(existing []coredatasource.RecordRef, seen map[string]bool, candidates ...coredatasource.RecordRef) []coredatasource.RecordRef {
	for _, candidate := range candidates {
		key := refSortKey(candidate)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		existing = append(existing, candidate)
	}
	return existing
}

func refSortKey(ref coredatasource.RecordRef) string {
	return strings.Join([]string{
		string(ref.Datasource),
		string(ref.Entity),
		ref.ID,
		ref.Query,
		ref.URL,
		ref.SourceText,
	}, "\x00")
}

func confidence(value float64) float64 {
	if value <= 0 {
		return 0.5
	}
	if value > 1 {
		return 1
	}
	return value
}

func truncateBytes(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	limit := max
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
