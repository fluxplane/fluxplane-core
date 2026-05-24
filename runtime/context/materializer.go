package context

import (
	stdcontext "context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
)

// Materializer renders providers into change-only context diffs.
type Materializer struct {
	providers []corecontext.Provider
	records   map[corecontext.ProviderName]corecontext.ProviderRenderRecord
}

// NewMaterializer returns a context materializer initialized with previous
// committed render records.
func NewMaterializer(providers []corecontext.Provider, previous map[corecontext.ProviderName]corecontext.ProviderRenderRecord) *Materializer {
	m := &Materializer{
		providers: append([]corecontext.Provider(nil), providers...),
		records:   cloneRecords(previous),
	}
	sort.SliceStable(m.providers, func(i, j int) bool {
		return m.providers[i].Spec().Name < m.providers[j].Spec().Name
	})
	return m
}

// Records returns the current committed render records.
func (m *Materializer) Records() map[corecontext.ProviderName]corecontext.ProviderRenderRecord {
	if m == nil {
		return nil
	}
	return cloneRecords(m.records)
}

// Build renders all providers and commits the resulting in-memory records.
func (m *Materializer) Build(ctx stdcontext.Context, req corecontext.BuildRequest) (corecontext.BuildResult, error) {
	if m == nil {
		return corecontext.BuildResult{}, fmt.Errorf("context: materializer is nil")
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	previous := cloneRecords(m.records)
	if req.Previous != nil {
		previous = cloneRecords(req.Previous)
	}
	next := cloneRecords(previous)
	result := corecontext.BuildResult{
		TurnID:  req.TurnID,
		Reason:  req.Reason,
		Records: next,
	}
	for _, provider := range m.providers {
		if provider == nil {
			continue
		}
		spec := provider.Spec()
		if err := spec.Validate(); err != nil {
			return corecontext.BuildResult{}, err
		}
		name := spec.Name
		prev, hasPrevious := previous[name]
		providerReq := corecontext.Request{
			ThreadID:      req.ThreadID,
			BranchID:      req.BranchID,
			TurnID:        req.TurnID,
			Reason:        req.Reason,
			InputText:     req.InputText,
			RecentContext: req.RecentContext,
			Scope:         cloneStringMap(req.Scope),
			Observations:  append([]coreevidence.Observation(nil), req.Observations...),
			BudgetTokens:  req.BudgetTokens,
		}
		if hasPrevious {
			copied := cloneRecord(prev)
			providerReq.Previous = &copied
		}
		if fast, ok := provider.(corecontext.FingerprintingProvider); ok && hasPrevious {
			fingerprint, valid, err := fast.StateFingerprint(ctx, providerReq)
			if err != nil {
				return corecontext.BuildResult{}, fmt.Errorf("context provider %q fingerprint: %w", name, err)
			}
			if valid && fingerprint != "" && fingerprint == prev.Fingerprint {
				record := cloneRecord(prev)
				next[name] = record
				result.Providers = append(result.Providers, corecontext.ProviderDiff{
					Provider: name,
					Record:   record,
					Skipped:  true,
				})
				result.Active = append(result.Active, activeBlocks(record)...)
				continue
			}
		}
		blocks, err := provider.Build(ctx, providerReq)
		if err != nil {
			return corecontext.BuildResult{}, fmt.Errorf("context provider %q: %w", name, err)
		}
		record, diff, err := buildProviderRecord(name, spec.DefaultPlacement, prev, blocks)
		if err != nil {
			return corecontext.BuildResult{}, fmt.Errorf("context provider %q: %w", name, err)
		}
		next[name] = cloneRecord(record)
		result.Providers = append(result.Providers, diff)
		result.Added = append(result.Added, diff.Added...)
		result.Updated = append(result.Updated, diff.Updated...)
		result.Removed = append(result.Removed, diff.Removed...)
		result.Active = append(result.Active, activeBlocks(record)...)
	}
	result.Records = cloneRecords(next)
	m.records = cloneRecords(next)
	return result, nil
}

func buildProviderRecord(name corecontext.ProviderName, defaultPlacement corecontext.Placement, previous corecontext.ProviderRenderRecord, blocks []corecontext.Block) (corecontext.ProviderRenderRecord, corecontext.ProviderDiff, error) {
	normalized, err := normalizeBlocks(name, defaultPlacement, blocks)
	if err != nil {
		return corecontext.ProviderRenderRecord{}, corecontext.ProviderDiff{}, err
	}
	record := corecontext.ProviderRenderRecord{
		Provider: name,
		Blocks:   make(map[string]corecontext.RenderedBlockRecord, len(normalized)),
	}
	diff := corecontext.ProviderDiff{Provider: name}
	for _, block := range normalized {
		fingerprint := BlockFingerprint(block)
		id := block.ID
		rendered := corecontext.RenderedBlockRecord{ID: id, Fingerprint: fingerprint, Block: block}
		record.Blocks[id] = rendered
		prev, ok := previous.Blocks[id]
		switch {
		case !ok || prev.Removed:
			diff.Added = append(diff.Added, block)
		case prev.Fingerprint != fingerprint:
			diff.Updated = append(diff.Updated, block)
		}
	}
	for id, prev := range previous.Blocks {
		if prev.Removed {
			continue
		}
		if _, ok := record.Blocks[id]; ok {
			continue
		}
		removed := corecontext.BlockRemoved{
			Provider:            name,
			ID:                  id,
			Placement:           corecontext.NormalizePlacement(prev.Block.Placement),
			PreviousFingerprint: prev.Fingerprint,
		}
		diff.Removed = append(diff.Removed, removed)
		record.Blocks[id] = corecontext.RenderedBlockRecord{
			ID:          id,
			Fingerprint: prev.Fingerprint,
			Block:       prev.Block,
			Removed:     true,
		}
	}
	record.Fingerprint = ProviderFingerprint(activeBlocks(record))
	diff.Record = record
	return record, diff, nil
}

func normalizeBlocks(provider corecontext.ProviderName, defaultPlacement corecontext.Placement, blocks []corecontext.Block) ([]corecontext.Block, error) {
	out := make([]corecontext.Block, 0, len(blocks))
	seen := map[string]struct{}{}
	for i, block := range blocks {
		if strings.TrimSpace(block.Content) == "" && strings.TrimSpace(block.URI) == "" {
			continue
		}
		if block.Provider == "" {
			block.Provider = provider
		}
		if block.ID == "" {
			block.ID = fmt.Sprintf("%s/%d", provider, len(out)+1)
		}
		if block.Placement == "" {
			block.Placement = defaultPlacement
		}
		block.Placement = corecontext.NormalizePlacement(block.Placement)
		if _, ok := seen[block.ID]; ok {
			return nil, fmt.Errorf("duplicate block id %q", block.ID)
		}
		seen[block.ID] = struct{}{}
		if strings.TrimSpace(string(block.Provider)) == "" {
			return nil, fmt.Errorf("block %d provider is empty", i+1)
		}
		out = append(out, block)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// BlockFingerprint returns a stable fingerprint for the provider-visible block.
func BlockFingerprint(block corecontext.Block) string {
	data, _ := json.Marshal(block)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ProviderFingerprint returns a stable fingerprint for an ordered active block set.
func ProviderFingerprint(blocks []corecontext.Block) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, block.ID+"\x00"+BlockFingerprint(block))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func activeBlocks(record corecontext.ProviderRenderRecord) []corecontext.Block {
	if len(record.Blocks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(record.Blocks))
	for id, block := range record.Blocks {
		if !block.Removed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]corecontext.Block, 0, len(ids))
	for _, id := range ids {
		out = append(out, record.Blocks[id].Block)
	}
	return out
}

func cloneRecords(records map[corecontext.ProviderName]corecontext.ProviderRenderRecord) map[corecontext.ProviderName]corecontext.ProviderRenderRecord {
	out := make(map[corecontext.ProviderName]corecontext.ProviderRenderRecord, len(records))
	for key, record := range records {
		out[key] = cloneRecord(record)
	}
	return out
}

func cloneRecord(record corecontext.ProviderRenderRecord) corecontext.ProviderRenderRecord {
	out := record
	if record.Blocks != nil {
		out.Blocks = make(map[string]corecontext.RenderedBlockRecord, len(record.Blocks))
		for key, block := range record.Blocks {
			out.Blocks[key] = cloneRenderedBlock(block)
		}
	}
	return out
}

func cloneRenderedBlock(record corecontext.RenderedBlockRecord) corecontext.RenderedBlockRecord {
	out := record
	out.Block.Metadata = cloneStringMap(record.Block.Metadata)
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
