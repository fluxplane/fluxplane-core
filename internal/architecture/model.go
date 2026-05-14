package architecture

import (
	"fmt"
	"sort"
	"strings"
)

const DefaultModulePath = "github.com/fluxplane/agentruntime"

type Layer string

const (
	LayerCore          Layer = "core"
	LayerSDK           Layer = "sdk"
	LayerRuntime       Layer = "runtime"
	LayerOrchestration Layer = "orchestration"
	LayerAdapters      Layer = "adapters"
	LayerPlugins       Layer = "plugins"
	LayerApps          Layer = "apps"
	LayerFacade        Layer = "facade"
)

type ListedPackage struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type Config struct {
	ModulePath   string
	IncludeTests bool
}

type Report struct {
	ModulePath string          `json:"module_path"`
	Summary    Summary         `json:"summary"`
	Layers     []LayerSummary  `json:"layers"`
	Packages   []PackageReport `json:"packages"`
	Edges      []Edge          `json:"edges"`
	Violations []Violation     `json:"violations"`
}

type Summary struct {
	Score               int            `json:"score"`
	PackageCount        int            `json:"package_count"`
	InternalEdgeCount   int            `json:"internal_edge_count"`
	ViolationCount      int            `json:"violation_count"`
	CrossLayerEdges     int            `json:"cross_layer_edges"`
	SameLayerEdges      int            `json:"same_layer_edges"`
	RuntimeSiblingEdges int            `json:"runtime_sibling_edges"`
	MaxFanIn            int            `json:"max_fan_in"`
	MaxFanOut           int            `json:"max_fan_out"`
	ScorePenalties      []ScorePenalty `json:"score_penalties,omitempty"`
}

// ScorePenalty explains one contribution to the architecture score penalty.
type ScorePenalty struct {
	Kind      string `json:"kind"`
	Package   string `json:"package,omitempty"`
	Layer     Layer  `json:"layer,omitempty"`
	Count     int    `json:"count,omitempty"`
	Threshold int    `json:"threshold,omitempty"`
	Penalty   int    `json:"penalty"`
	Reason    string `json:"reason"`
}

type LayerSummary struct {
	Layer    Layer `json:"layer"`
	Packages int   `json:"packages"`
	FanIn    int   `json:"fan_in"`
	FanOut   int   `json:"fan_out"`
}

type PackageReport struct {
	ImportPath string `json:"import_path"`
	Layer      Layer  `json:"layer"`
	FanIn      int    `json:"fan_in"`
	FanOut     int    `json:"fan_out"`
}

type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	FromLayer Layer  `json:"from_layer"`
	ToLayer   Layer  `json:"to_layer"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	TestOnly  bool   `json:"test_only,omitempty"`
}

type Violation struct {
	From      string `json:"from"`
	To        string `json:"to"`
	FromLayer Layer  `json:"from_layer"`
	ToLayer   Layer  `json:"to_layer"`
	Reason    string `json:"reason"`
}

func Analyze(pkgs []ListedPackage, cfg Config) Report {
	modulePath := cfg.ModulePath
	if modulePath == "" {
		modulePath = DefaultModulePath
	}

	known := make(map[string]struct{}, len(pkgs))
	for _, pkg := range pkgs {
		if layerOf(modulePath, pkg.ImportPath) != "" {
			known[pkg.ImportPath] = struct{}{}
		}
	}

	fanIn := make(map[string]int)
	fanOut := make(map[string]int)
	layerStats := map[Layer]*LayerSummary{}
	var edges []Edge
	var violations []Violation

	for _, pkg := range pkgs {
		fromLayer := layerOf(modulePath, pkg.ImportPath)
		if fromLayer == "" {
			continue
		}
		ensureLayer(layerStats, fromLayer).Packages++

		seen := map[string]bool{}
		addImports := func(imports []string, testOnly bool) {
			for _, imported := range imports {
				if imported == pkg.ImportPath {
					continue
				}
				toLayer := layerOf(modulePath, imported)
				if toLayer == "" {
					continue
				}
				if _, ok := known[imported]; !ok {
					continue
				}
				key := imported
				if testOnly {
					key += " test"
				}
				if seen[key] {
					continue
				}
				seen[key] = true

				allowed, reason := allowedImport(fromLayer, toLayer)
				edge := Edge{
					From:      pkg.ImportPath,
					To:        imported,
					FromLayer: fromLayer,
					ToLayer:   toLayer,
					Allowed:   allowed,
					Reason:    reason,
					TestOnly:  testOnly,
				}
				edges = append(edges, edge)
				fanOut[pkg.ImportPath]++
				fanIn[imported]++
				ensureLayer(layerStats, fromLayer).FanOut++
				ensureLayer(layerStats, toLayer).FanIn++
				if !allowed {
					violations = append(violations, Violation{
						From:      edge.From,
						To:        edge.To,
						FromLayer: edge.FromLayer,
						ToLayer:   edge.ToLayer,
						Reason:    edge.Reason,
					})
				}
			}
		}

		addImports(pkg.Imports, false)
		if cfg.IncludeTests {
			addImports(pkg.TestImports, true)
			addImports(pkg.XTestImports, true)
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].From == violations[j].From {
			return violations[i].To < violations[j].To
		}
		return violations[i].From < violations[j].From
	})

	var packageReports []PackageReport
	for _, pkg := range pkgs {
		layer := layerOf(modulePath, pkg.ImportPath)
		if layer == "" {
			continue
		}
		packageReports = append(packageReports, PackageReport{
			ImportPath: pkg.ImportPath,
			Layer:      layer,
			FanIn:      fanIn[pkg.ImportPath],
			FanOut:     fanOut[pkg.ImportPath],
		})
	}
	sort.Slice(packageReports, func(i, j int) bool {
		return packageReports[i].ImportPath < packageReports[j].ImportPath
	})

	var summaries []LayerSummary
	for _, layer := range layerOrder() {
		if summary, ok := layerStats[layer]; ok {
			summaries = append(summaries, *summary)
		}
	}

	summary := summarize(packageReports, edges, violations)
	if edges == nil {
		edges = []Edge{}
	}
	if violations == nil {
		violations = []Violation{}
	}
	return Report{
		ModulePath: modulePath,
		Summary:    summary,
		Layers:     summaries,
		Packages:   packageReports,
		Edges:      edges,
		Violations: violations,
	}
}

func layerOf(modulePath, importPath string) Layer {
	if importPath == modulePath {
		return LayerFacade
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(importPath, prefix)
	layer, _, _ := strings.Cut(rest, "/")
	switch Layer(layer) {
	case LayerCore, LayerSDK, LayerRuntime, LayerOrchestration, LayerAdapters, LayerPlugins, LayerApps:
		return Layer(layer)
	default:
		return ""
	}
}

func allowedImport(from, to Layer) (bool, string) {
	allowed := map[Layer]map[Layer]bool{
		LayerCore: {
			LayerCore: true,
		},
		LayerSDK: {
			LayerCore: true,
			LayerSDK:  true,
		},
		LayerRuntime: {
			LayerCore:    true,
			LayerRuntime: true,
		},
		LayerOrchestration: {
			LayerCore:          true,
			LayerRuntime:       true,
			LayerOrchestration: true,
		},
		LayerAdapters: {
			LayerCore:          true,
			LayerRuntime:       true,
			LayerOrchestration: true,
			LayerAdapters:      true,
		},
		LayerPlugins: {
			LayerCore:          true,
			LayerSDK:           true,
			LayerRuntime:       true,
			LayerOrchestration: true,
			LayerAdapters:      true,
			LayerPlugins:       true,
		},
		LayerApps: {
			LayerCore:          true,
			LayerSDK:           true,
			LayerRuntime:       true,
			LayerOrchestration: true,
			LayerAdapters:      true,
			LayerPlugins:       true,
			LayerApps:          true,
			LayerFacade:        true,
		},
		LayerFacade: {
			LayerCore:          true,
			LayerSDK:           true,
			LayerRuntime:       true,
			LayerOrchestration: true,
			LayerAdapters:      true,
		},
	}
	if allowed[from][to] {
		return true, ""
	}
	return false, fmt.Sprintf("%s may not import %s", from, to)
}

func summarize(pkgs []PackageReport, edges []Edge, violations []Violation) Summary {
	summary := Summary{
		PackageCount:      len(pkgs),
		InternalEdgeCount: len(edges),
		ViolationCount:    len(violations),
	}
	for _, edge := range edges {
		if edge.FromLayer == edge.ToLayer {
			summary.SameLayerEdges++
			if edge.FromLayer == LayerRuntime {
				summary.RuntimeSiblingEdges++
			}
		} else {
			summary.CrossLayerEdges++
		}
	}
	for _, pkg := range pkgs {
		if pkg.FanIn > summary.MaxFanIn {
			summary.MaxFanIn = pkg.FanIn
		}
		if pkg.FanOut > summary.MaxFanOut {
			summary.MaxFanOut = pkg.FanOut
		}
	}

	var penalties []ScorePenalty
	penalty := 0
	if summary.ViolationCount > 0 {
		amount := summary.ViolationCount * 25
		penalty += amount
		penalties = append(penalties, ScorePenalty{
			Kind:    "violation",
			Count:   summary.ViolationCount,
			Penalty: amount,
			Reason:  "boundary violations are hard architecture failures",
		})
	}
	if summary.RuntimeSiblingEdges > 0 {
		amount := summary.RuntimeSiblingEdges * 2
		penalty += amount
		penalties = append(penalties, ScorePenalty{
			Kind:    "runtime_sibling",
			Layer:   LayerRuntime,
			Count:   summary.RuntimeSiblingEdges,
			Penalty: amount,
			Reason:  "runtime sibling imports usually indicate unclear execution composition",
		})
	}
	for _, pkg := range pkgs {
		if pkg.Layer != LayerCore && pkg.Layer != LayerRuntime && pkg.Layer != LayerOrchestration {
			continue
		}
		if pkg.FanOut > 12 {
			amount := (pkg.FanOut - 12) * 2
			penalty += amount
			penalties = append(penalties, ScorePenalty{
				Kind:      "fan_out",
				Package:   pkg.ImportPath,
				Layer:     pkg.Layer,
				Count:     pkg.FanOut,
				Threshold: 12,
				Penalty:   amount,
				Reason:    "high fan-out in inner/use-case layers suggests splitting or moving composition outward",
			})
		}
	}
	summary.Score = 100 - penalty
	if summary.Score < 0 {
		summary.Score = 0
	}
	summary.ScorePenalties = penalties
	return summary
}

func ensureLayer(stats map[Layer]*LayerSummary, layer Layer) *LayerSummary {
	if stats[layer] == nil {
		stats[layer] = &LayerSummary{Layer: layer}
	}
	return stats[layer]
}

func layerOrder() []Layer {
	return []Layer{
		LayerCore,
		LayerSDK,
		LayerRuntime,
		LayerOrchestration,
		LayerAdapters,
		LayerPlugins,
		LayerApps,
		LayerFacade,
	}
}
