package context

import (
	stdcontext "context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	"sort"
	"strings"
)

// Materializer renders providers into change-only context diffs.
type Materializer struct {
	providers []Provider
	records   map[ProviderName]ProviderRenderRecord
}

// NewMaterializer returns a context materializer initialized with previous
// committed render records.
func NewMaterializer(providers []Provider, previous map[ProviderName]ProviderRenderRecord) *Materializer {
	m := &Materializer{
		providers: append([]Provider(nil), providers...),
		records:   cloneRecords(previous),
	}
	sort.SliceStable(m.providers, func(i, j int) bool {
		return m.providers[i].Spec().Name < m.providers[j].Spec().Name
	})
	return m
}

// Records returns the current committed render records.
func (m *Materializer) Records() map[ProviderName]ProviderRenderRecord {
	if m == nil {
		return nil
	}
	return cloneRecords(m.records)
}

// Build renders all providers and commits the resulting in-memory records.
func (m *Materializer) Build(ctx stdcontext.Context, req BuildRequest) (BuildResult, error) {
	if m == nil {
		return BuildResult{}, fmt.Errorf("context: materializer is nil")
	}
	if ctx == nil {
		ctx = stdcontext.Background()
	}
	previous := cloneRecords(m.records)
	if req.Previous != nil {
		previous = cloneRecords(req.Previous)
	}
	next := cloneRecords(previous)
	result := BuildResult{
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
			return BuildResult{}, err
		}
		name := spec.Name
		prev, hasPrevious := previous[name]
		providerReq := Request{
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
		if fast, ok := provider.(FingerprintingProvider); ok && hasPrevious {
			fingerprint, valid, err := fast.StateFingerprint(ctx, providerReq)
			if err != nil {
				return BuildResult{}, fmt.Errorf("context provider %q fingerprint: %w", name, err)
			}
			if valid && fingerprint != "" && fingerprint == prev.Fingerprint {
				record := cloneRecord(prev)
				next[name] = record
				result.Providers = append(result.Providers, ProviderDiff{
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
			return BuildResult{}, fmt.Errorf("context provider %q: %w", name, err)
		}
		record, diff, err := buildProviderRecord(name, spec.DefaultPlacement, prev, blocks)
		if err != nil {
			return BuildResult{}, fmt.Errorf("context provider %q: %w", name, err)
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

func buildProviderRecord(name ProviderName, defaultPlacement Placement, previous ProviderRenderRecord, blocks []Block) (ProviderRenderRecord, ProviderDiff, error) {
	normalized, err := normalizeBlocks(name, defaultPlacement, blocks)
	if err != nil {
		return ProviderRenderRecord{}, ProviderDiff{}, err
	}
	record := ProviderRenderRecord{
		Provider: name,
		Blocks:   make(map[string]RenderedBlockRecord, len(normalized)),
	}
	diff := ProviderDiff{Provider: name}
	for _, block := range normalized {
		fingerprint := BlockFingerprint(block)
		id := block.ID
		rendered := RenderedBlockRecord{ID: id, Fingerprint: fingerprint, Block: block}
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
		removed := BlockRemoved{
			Provider:            name,
			ID:                  id,
			Placement:           NormalizePlacement(prev.Block.Placement),
			PreviousFingerprint: prev.Fingerprint,
		}
		diff.Removed = append(diff.Removed, removed)
		record.Blocks[id] = RenderedBlockRecord{
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

func normalizeBlocks(provider ProviderName, defaultPlacement Placement, blocks []Block) ([]Block, error) {
	out := make([]Block, 0, len(blocks))
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
		block.Placement = NormalizePlacement(block.Placement)
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
func BlockFingerprint(block Block) string {
	data, _ := json.Marshal(block)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ProviderFingerprint returns a stable fingerprint for an ordered active block set.
func ProviderFingerprint(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, block.ID+"\x00"+BlockFingerprint(block))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func activeBlocks(record ProviderRenderRecord) []Block {
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
	out := make([]Block, 0, len(ids))
	for _, id := range ids {
		out = append(out, record.Blocks[id].Block)
	}
	return out
}

func cloneRecords(records map[ProviderName]ProviderRenderRecord) map[ProviderName]ProviderRenderRecord {
	out := make(map[ProviderName]ProviderRenderRecord, len(records))
	for key, record := range records {
		out[key] = cloneRecord(record)
	}
	return out
}

func cloneRecord(record ProviderRenderRecord) ProviderRenderRecord {
	out := record
	if record.Blocks != nil {
		out.Blocks = make(map[string]RenderedBlockRecord, len(record.Blocks))
		for key, block := range record.Blocks {
			out.Blocks[key] = cloneRenderedBlock(block)
		}
	}
	return out
}

func cloneRenderedBlock(record RenderedBlockRecord) RenderedBlockRecord {
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
