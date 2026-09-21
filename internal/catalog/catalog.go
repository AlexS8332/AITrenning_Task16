// Package catalog — локальный справочник по животным: данные вшиты в
// бинарник, сеть не нужна. На нём работает половина инструментов сервера,
// поэтому сервер можно проверить на машине без интернета.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

//go:embed animals.json
var animalsJSON []byte

// Range — числовой диапазон: вес и длина у животных всегда «от и до».
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

func (r Range) String() string {
	if r.Min == r.Max {
		return trimFloat(r.Min)
	}
	return trimFloat(r.Min) + "–" + trimFloat(r.Max)
}

// Animal — запись справочника.
type Animal struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	ScientificName string   `json:"scientific_name"`
	Class          string   `json:"class"`
	Order          string   `json:"order"`
	Family         string   `json:"family"`
	Diet           string   `json:"diet"`
	Habitats       []string `json:"habitats"`
	Regions        []string `json:"regions"`
	Food           []string `json:"food"`
	WeightKg       Range    `json:"weight_kg"`
	LengthCm       Range    `json:"length_cm"`
	LifespanYears  int      `json:"lifespan_years"`
	Conservation   string   `json:"conservation_status"`
	Description    string   `json:"description"`
}

// Brief — короткая запись для списков: в ответ на list_animals не нужно
// отдавать всё, иначе список из двадцати карточек раздувает контекст.
type Brief struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ScientificName string `json:"scientific_name"`
	Class          string `json:"class"`
	Diet           string `json:"diet"`
}

func (a Animal) Brief() Brief {
	return Brief{
		ID:             a.ID,
		Name:           a.Name,
		ScientificName: a.ScientificName,
		Class:          a.Class,
		Diet:           a.Diet,
	}
}

// Catalog — загруженный справочник.
type Catalog struct {
	animals []Animal
	byID    map[string]Animal
	rnd     *rand.Rand
}

// Load разбирает вшитые данные. Ошибка здесь означает битый JSON в
// исходниках, то есть ошибку сборки, а не ввода.
func Load() (*Catalog, error) {
	var animals []Animal
	if err := json.Unmarshal(animalsJSON, &animals); err != nil {
		return nil, fmt.Errorf("справочник не разобрался: %w", err)
	}
	if len(animals) == 0 {
		return nil, fmt.Errorf("справочник пуст")
	}

	c := &Catalog{
		animals: animals,
		byID:    make(map[string]Animal, len(animals)),
		rnd:     rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}
	for _, a := range animals {
		if _, dup := c.byID[a.ID]; dup {
			return nil, fmt.Errorf("повторяющийся id %q", a.ID)
		}
		c.byID[a.ID] = a
	}
	return c, nil
}

// MustLoad — для main и тестов: справочник либо есть, либо программа
// бессмысленна.
func MustLoad() *Catalog {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// Len — число записей.
func (c *Catalog) Len() int { return len(c.animals) }

// All — все записи в порядке файла.
func (c *Catalog) All() []Animal {
	out := make([]Animal, len(c.animals))
	copy(out, c.animals)
	return out
}

// Classes — классы животных справочника по алфавиту. Нужны инструменту
// list_animals: без них модель не знает, какие значения допустимы.
func (c *Catalog) Classes() []string { return c.values(func(a Animal) string { return a.Class }) }

// Diets — типы питания по алфавиту.
func (c *Catalog) Diets() []string { return c.values(func(a Animal) string { return a.Diet }) }

// Habitats — все среды обитания по алфавиту.
func (c *Catalog) Habitats() []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, a := range c.animals {
		for _, h := range a.Habitats {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) values(get func(Animal) string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0)
	for _, a := range c.animals {
		v := get(a)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// Get ищет животное по id, а если такого нет — по названию (русскому или
// латинскому, без учёта регистра). Модель одинаково охотно присылает и то
// и другое, и отказывать из-за формы имени незачем.
func (c *Catalog) Get(idOrName string) (Animal, bool) {
	key := strings.TrimSpace(idOrName)
	if key == "" {
		return Animal{}, false
	}
	if a, ok := c.byID[strings.ToLower(key)]; ok {
		return a, true
	}
	lower := strings.ToLower(key)
	for _, a := range c.animals {
		if strings.ToLower(a.Name) == lower || strings.ToLower(a.ScientificName) == lower {
			return a, true
		}
	}
	return Animal{}, false
}

// Filter — условия отбора. Пустые поля не ограничивают ничего.
type Filter struct {
	Query       string
	Class       string
	Diet        string
	Habitat     string
	MinWeightKg *float64
	MaxWeightKg *float64
}

// Find отбирает записи по фильтру, сохраняя порядок справочника.
// Строковые поля сравниваются по вхождению без учёта регистра: «лес»
// находит и «смешанный лес», и «лиственный лес».
func (c *Catalog) Find(f Filter) []Animal {
	out := make([]Animal, 0)
	for _, a := range c.animals {
		if !a.matches(f) {
			continue
		}
		out = append(out, a)
	}
	return out
}

func (a Animal) matches(f Filter) bool {
	if q := norm(f.Query); q != "" && !a.textMatch(q) {
		return false
	}
	if v := norm(f.Class); v != "" && !strings.Contains(norm(a.Class), v) {
		return false
	}
	if v := norm(f.Diet); v != "" && !strings.Contains(norm(a.Diet), v) {
		return false
	}
	if v := norm(f.Habitat); v != "" && !containsAny(a.Habitats, v) && !containsAny(a.Regions, v) {
		return false
	}
	// Диапазоны сравниваются по пересечению, а не по середине: рысь
	// весом 18–30 кг подходит и под «тяжелее 25», и под «легче 20».
	if f.MinWeightKg != nil && a.WeightKg.Max < *f.MinWeightKg {
		return false
	}
	if f.MaxWeightKg != nil && a.WeightKg.Min > *f.MaxWeightKg {
		return false
	}
	return true
}

func (a Animal) textMatch(q string) bool {
	fields := []string{a.ID, a.Name, a.ScientificName, a.Class, a.Order, a.Family, a.Description}
	for _, f := range fields {
		if strings.Contains(norm(f), q) {
			return true
		}
	}
	return containsAny(a.Habitats, q) || containsAny(a.Regions, q) || containsAny(a.Food, q)
}

// Random возвращает случайную запись.
func (c *Catalog) Random() Animal {
	return c.animals[c.rnd.IntN(len(c.animals))]
}

// FieldNames — поля, по которым умеет сравнивать Compare.
var FieldNames = []string{
	"scientific_name", "class", "order", "family", "diet",
	"habitats", "regions", "food", "weight_kg", "length_cm",
	"lifespan_years", "conservation_status",
}

// Field возвращает значение поля строкой для таблицы сравнения.
func (a Animal) Field(name string) (string, bool) {
	switch name {
	case "scientific_name":
		return a.ScientificName, true
	case "class":
		return a.Class, true
	case "order":
		return a.Order, true
	case "family":
		return a.Family, true
	case "diet":
		return a.Diet, true
	case "habitats":
		return strings.Join(a.Habitats, ", "), true
	case "regions":
		return strings.Join(a.Regions, ", "), true
	case "food":
		return strings.Join(a.Food, ", "), true
	case "weight_kg":
		return a.WeightKg.String(), true
	case "length_cm":
		return a.LengthCm.String(), true
	case "lifespan_years":
		return fmt.Sprint(a.LifespanYears), true
	case "conservation_status":
		return a.Conservation, true
	}
	return "", false
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func containsAny(values []string, q string) bool {
	for _, v := range values {
		if strings.Contains(norm(v), q) {
			return true
		}
	}
	return false
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}
