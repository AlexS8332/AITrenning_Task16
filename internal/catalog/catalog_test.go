package catalog

import (
	"testing"
)

func load(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatalf("справочник не загрузился: %v", err)
	}
	return c
}

func TestLoadNotEmptyAndConsistent(t *testing.T) {
	c := load(t)
	if c.Len() < 10 {
		t.Fatalf("записей %d, ожидалось хотя бы 10", c.Len())
	}

	for _, a := range c.All() {
		if a.ID == "" || a.Name == "" || a.ScientificName == "" {
			t.Errorf("неполная запись: %+v", a)
		}
		if a.WeightKg.Min > a.WeightKg.Max {
			t.Errorf("%s: вес от %v до %v", a.ID, a.WeightKg.Min, a.WeightKg.Max)
		}
	}
}

func TestGetByIDAndNames(t *testing.T) {
	c := load(t)

	for _, key := range []string{"lynx", "Обыкновенная рысь", "обыкновенная рысь", "Lynx lynx"} {
		a, ok := c.Get(key)
		if !ok {
			t.Fatalf("по ключу %q запись не нашлась", key)
		}
		if a.ID != "lynx" {
			t.Errorf("по ключу %q нашлось %q", key, a.ID)
		}
	}

	if _, ok := c.Get("малая выхухоль"); ok {
		t.Error("нашлось животное, которого в справочнике нет")
	}
	if _, ok := c.Get("   "); ok {
		t.Error("пустой запрос не должен ничего находить")
	}
}

func TestFindByClassAndDiet(t *testing.T) {
	c := load(t)

	birds := c.Find(Filter{Class: "птицы"})
	if len(birds) == 0 {
		t.Fatal("птиц не нашлось")
	}
	for _, a := range birds {
		if a.Class != "птицы" {
			t.Errorf("%s: класс %q", a.ID, a.Class)
		}
	}

	predators := c.Find(Filter{Class: "птицы", Diet: "хищник"})
	if len(predators) == 0 || len(predators) > len(birds) {
		t.Fatalf("хищных птиц %d при %d птицах", len(predators), len(birds))
	}
}

func TestFindByHabitatSubstring(t *testing.T) {
	c := load(t)

	// «лес» должен находить и «смешанный лес», и «лиственный лес».
	forest := c.Find(Filter{Habitat: "лес"})
	if len(forest) < 2 {
		t.Fatalf("по «лес» нашлось %d записей", len(forest))
	}

	found := false
	for _, a := range forest {
		if a.ID == "lynx" {
			found = true
		}
	}
	if !found {
		t.Error("рысь не нашлась по среде обитания «лес»")
	}
}

func TestFindWeightRangeOverlap(t *testing.T) {
	c := load(t)

	lynx, _ := c.Get("lynx") // 18–30 кг
	min, max := 25.0, 20.0

	heavy := c.Find(Filter{Query: "рысь", MinWeightKg: &min})
	if len(heavy) != 1 || heavy[0].ID != lynx.ID {
		t.Errorf("рысь не прошла фильтр «тяжелее 25 кг»: %v", heavy)
	}

	light := c.Find(Filter{Query: "рысь", MaxWeightKg: &max})
	if len(light) != 1 || light[0].ID != lynx.ID {
		t.Errorf("рысь не прошла фильтр «легче 20 кг»: %v", light)
	}

	tiny := 0.5
	if got := c.Find(Filter{Query: "рысь", MaxWeightKg: &tiny}); len(got) != 0 {
		t.Errorf("рысь прошла фильтр «легче 0.5 кг»: %v", got)
	}
}

func TestFieldKnowsAllDeclaredFields(t *testing.T) {
	c := load(t)
	a, _ := c.Get("lynx")

	for _, name := range FieldNames {
		v, ok := a.Field(name)
		if !ok {
			t.Errorf("поле %q не поддерживается", name)
			continue
		}
		if v == "" {
			t.Errorf("поле %q пустое", name)
		}
	}
	if _, ok := a.Field("цвет_глаз"); ok {
		t.Error("несуществующее поле вернулось как известное")
	}
}

func TestRangeString(t *testing.T) {
	cases := []struct {
		in   Range
		want string
	}{
		{Range{18, 30}, "18–30"},
		{Range{0.6, 1.3}, "0.6–1.3"},
		{Range{5, 5}, "5"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%v: получили %q, ожидали %q", c.in, got, c.want)
		}
	}
}

func TestRandomReturnsCatalogEntry(t *testing.T) {
	c := load(t)
	for range 20 {
		a := c.Random()
		if _, ok := c.Get(a.ID); !ok {
			t.Fatalf("случайная запись %q не из справочника", a.ID)
		}
	}
}
