package project

import "testing"

func TestProjectValidateRejectsEmptyIdentity(t *testing.T) {
	if err := (Project{}).Validate(); err == nil {
		t.Fatal("Validate: want error for empty project")
	}
}

func TestProjectValidateRejectsDuplicateFacet(t *testing.T) {
	err := Project{
		ID:   "project:.",
		Root: ".",
		Facets: []Facet{
			{Kind: FacetGoModule, Manifest: Manifest{Path: "go.mod"}},
			{Kind: FacetGoModule, Manifest: Manifest{Path: "go.mod"}},
		},
	}.Validate()
	if err == nil {
		t.Fatal("Validate: want duplicate facet error")
	}
}

func TestProjectValidateAcceptsMultiFacetProject(t *testing.T) {
	err := Project{
		ID:   "project:.",
		Root: ".",
		Facets: []Facet{
			{Kind: FacetGoModule, Manifest: Manifest{Path: "go.mod"}},
			{Kind: FacetNodePackage, Manifest: Manifest{Path: "package.json"}},
		},
	}.Validate()
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
