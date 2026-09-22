// Package catalog — справочник по животным в базе данных SQLite.
//
// База создаётся при первом запуске: схема и начальные данные вшиты в
// бинарник, поэтому сервер поднимается на чистой машине одной командой,
// а дальше живёт своей жизнью — записи можно добавлять, менять и
// удалять, и они переживают перезапуск.
package catalog

import (
	"fmt"
	"strings"
)

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

// Brief — короткая запись для списков: в ответ на список животных не
// нужно отдавать всё, иначе два десятка карточек раздувают контекст.
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

// Filter — условия отбора. Пустые поля не ограничивают ничего.
type Filter struct {
	Query       string
	Class       string
	Diet        string
	Habitat     string
	MinWeightKg *float64
	MaxWeightKg *float64
	Limit       int
	Offset      int
}

// Patch — изменение записи: заполненные поля заменяют старые значения,
// пустые оставляют как было. Поэтому все поля — указатели: иначе
// «стереть описание» и «не трогать описание» выглядели бы одинаково.
type Patch struct {
	Name           *string
	ScientificName *string
	Class          *string
	Order          *string
	Family         *string
	Diet           *string
	Habitats       *[]string
	Regions        *[]string
	Food           *[]string
	WeightKg       *Range
	LengthCm       *Range
	LifespanYears  *int
	Conservation   *string
	Description    *string
}

// Apply накладывает изменение на запись.
func (p Patch) Apply(a Animal) Animal {
	setString(&a.Name, p.Name)
	setString(&a.ScientificName, p.ScientificName)
	setString(&a.Class, p.Class)
	setString(&a.Order, p.Order)
	setString(&a.Family, p.Family)
	setString(&a.Diet, p.Diet)
	setString(&a.Conservation, p.Conservation)
	setString(&a.Description, p.Description)
	if p.Habitats != nil {
		a.Habitats = *p.Habitats
	}
	if p.Regions != nil {
		a.Regions = *p.Regions
	}
	if p.Food != nil {
		a.Food = *p.Food
	}
	if p.WeightKg != nil {
		a.WeightKg = *p.WeightKg
	}
	if p.LengthCm != nil {
		a.LengthCm = *p.LengthCm
	}
	if p.LifespanYears != nil {
		a.LifespanYears = *p.LifespanYears
	}
	return a
}

// Empty сообщает, что менять нечего: вызов без единого поля — ошибка
// вызывающего, а не пустая работа базы.
func (p Patch) Empty() bool {
	return p.Name == nil && p.ScientificName == nil && p.Class == nil &&
		p.Order == nil && p.Family == nil && p.Diet == nil &&
		p.Habitats == nil && p.Regions == nil && p.Food == nil &&
		p.WeightKg == nil && p.LengthCm == nil && p.LifespanYears == nil &&
		p.Conservation == nil && p.Description == nil
}

func setString(dst *string, src *string) {
	if src != nil {
		*dst = *src
	}
}

// Validate проверяет запись перед записью в базу. Аргументы приходят от
// модели или от человека через протокол, поэтому проверяем их как
// недоверенный ввод и отвечаем словами, а не ограничением базы.
func (a Animal) Validate() error {
	switch {
	case strings.TrimSpace(a.ID) == "":
		return fmt.Errorf("id пуст")
	case strings.TrimSpace(a.Name) == "":
		return fmt.Errorf("name пусто")
	case strings.TrimSpace(a.ScientificName) == "":
		return fmt.Errorf("scientific_name пусто")
	case strings.TrimSpace(a.Class) == "":
		return fmt.Errorf("class пусто")
	case a.WeightKg.Min < 0 || a.WeightKg.Max < 0 || a.LengthCm.Min < 0 || a.LengthCm.Max < 0:
		return fmt.Errorf("размеры не могут быть отрицательными")
	case a.WeightKg.Min > a.WeightKg.Max:
		return fmt.Errorf("вес: min %v больше max %v", a.WeightKg.Min, a.WeightKg.Max)
	case a.LengthCm.Min > a.LengthCm.Max:
		return fmt.Errorf("длина: min %v больше max %v", a.LengthCm.Min, a.LengthCm.Max)
	case a.LifespanYears < 0:
		return fmt.Errorf("продолжительность жизни не может быть отрицательной")
	}
	return nil
}

// FieldNames — поля, по которым умеет сравнивать compare_animals.
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

// norm — форма для поиска. strings.ToLower знает про Unicode, а lower()
// в SQLite — только про латиницу, поэтому нормализуем на стороне Go.
func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.4f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}
