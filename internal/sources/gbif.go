package sources

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const DefaultGBIFBase = "https://api.gbif.org/v1"

// GBIF — таксономическая база GBIF: сверка латинского названия, дерево
// классификации и народные названия. Ключ не нужен.
type GBIF struct {
	Base string
	f    *Fetcher
}

func NewGBIF(base string, f *Fetcher) *GBIF {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultGBIFBase
	}
	return &GBIF{Base: base, f: f}
}

// RankRu — русские названия рангов. Дерево классификации собирается из
// ответа GBIF программой, поэтому русские подписи нужны здесь, а не на
// стороне клиента.
var RankRu = map[string]string{
	"KINGDOM":    "царство",
	"PHYLUM":     "тип",
	"CLASS":      "класс",
	"ORDER":      "отряд",
	"FAMILY":     "семейство",
	"GENUS":      "род",
	"SPECIES":    "вид",
	"SUBSPECIES": "подвид",
	"SUBFAMILY":  "подсемейство",
	"SUBORDER":   "подотряд",
	"SUBCLASS":   "подкласс",
	"SUBPHYLUM":  "подтип",
}

// Match — результат сверки названия.
type Match struct {
	Found      bool   `json:"found"`
	UsageKey   int    `json:"usage_key,omitempty"`
	Canonical  string `json:"canonical_name,omitempty"`
	Scientific string `json:"scientific_name,omitempty"`
	Rank       string `json:"rank,omitempty"`
	Status     string `json:"status,omitempty"`
	Confidence int    `json:"confidence"`
	MatchType  string `json:"match_type"`
	Kingdom    string `json:"kingdom,omitempty"`
	Phylum     string `json:"phylum,omitempty"`
	Class      string `json:"class,omitempty"`
	Order      string `json:"order,omitempty"`
	Family     string `json:"family,omitempty"`
	Genus      string `json:"genus,omitempty"`
	Species    string `json:"species,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Match сверяет название. Нечёткое совпадение (FUZZY) и совпадение только
// по роду (HIGHERRANK) не считаются найденным таксоном: это как раз те
// случаи, где база «подсказывает похожее», а нам похожее не нужно.
func (g *GBIF) Match(ctx context.Context, name string) (Match, error) {
	q := url.Values{}
	q.Set("name", name)

	var resp struct {
		UsageKey       int    `json:"usageKey"`
		ScientificName string `json:"scientificName"`
		CanonicalName  string `json:"canonicalName"`
		Rank           string `json:"rank"`
		Status         string `json:"status"`
		Confidence     int    `json:"confidence"`
		MatchType      string `json:"matchType"`
		Kingdom        string `json:"kingdom"`
		Phylum         string `json:"phylum"`
		Class          string `json:"class"`
		Order          string `json:"order"`
		Family         string `json:"family"`
		Genus          string `json:"genus"`
		Species        string `json:"species"`
	}
	if err := g.f.GetJSON(ctx, g.Base+"/species/match?"+q.Encode(), &resp); err != nil {
		return Match{}, fmt.Errorf("сверка с GBIF: %w", err)
	}

	m := Match{
		Confidence: resp.Confidence,
		MatchType:  resp.MatchType,
	}
	switch resp.MatchType {
	case "EXACT":
		m.Found = true
	case "FUZZY":
		m.Note = "совпадение нечёткое: в базе есть похожее название, но не это"
	case "HIGHERRANK":
		m.Note = "совпал только более высокий ранг (например, род); такого вида в базе нет"
	default:
		m.Note = "таксон с таким названием в базе не найден"
	}
	if resp.MatchType != "NONE" {
		m.UsageKey = resp.UsageKey
		m.Canonical = resp.CanonicalName
		m.Scientific = resp.ScientificName
		m.Rank = resp.Rank
		m.Status = resp.Status
		m.Kingdom = resp.Kingdom
		m.Phylum = resp.Phylum
		m.Class = resp.Class
		m.Order = resp.Order
		m.Family = resp.Family
		m.Genus = resp.Genus
		m.Species = resp.Species
	}
	return m, nil
}

// TaxonNode — узел дерева классификации.
type TaxonNode struct {
	Rank   string `json:"rank"`
	RankRu string `json:"rank_ru"`
	Name   string `json:"name"`
	Key    int    `json:"key"`
}

// Tree собирает цепочку родителей и сам таксон.
func (g *GBIF) Tree(ctx context.Context, key int) ([]TaxonNode, error) {
	type taxon struct {
		Key           int    `json:"key"`
		Rank          string `json:"rank"`
		CanonicalName string `json:"canonicalName"`
	}

	var parents []taxon
	if err := g.f.GetJSON(ctx, fmt.Sprintf("%s/species/%d/parents", g.Base, key), &parents); err != nil {
		return nil, fmt.Errorf("родители таксона: %w", err)
	}
	var self taxon
	if err := g.f.GetJSON(ctx, fmt.Sprintf("%s/species/%d", g.Base, key), &self); err != nil {
		return nil, fmt.Errorf("таксон: %w", err)
	}
	if self.Key == 0 {
		return nil, fmt.Errorf("таксона с ключом %d в GBIF нет", key)
	}

	tree := make([]TaxonNode, 0, len(parents)+1)
	for _, p := range append(parents, self) {
		tree = append(tree, TaxonNode{
			Rank:   p.Rank,
			RankRu: RankRu[p.Rank],
			Name:   p.CanonicalName,
			Key:    p.Key,
		})
	}
	return tree, nil
}

// Vernacular возвращает народные названия без повторов, в порядке появления.
func (g *GBIF) Vernacular(ctx context.Context, key int, lang string) ([]string, error) {
	var resp struct {
		Results []struct {
			Name     string `json:"vernacularName"`
			Language string `json:"language"`
		} `json:"results"`
	}
	u := fmt.Sprintf("%s/species/%d/vernacularNames?limit=200", g.Base, key)
	if err := g.f.GetJSON(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("народные названия: %w", err)
	}

	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, r := range resp.Results {
		if !strings.EqualFold(r.Language, lang) {
			continue
		}
		name := strings.TrimSpace(r.Name)
		lower := strings.ToLower(name)
		if name == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		names = append(names, name)
	}
	return names, nil
}
