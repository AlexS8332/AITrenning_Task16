package catalog

import (
	"errors"
	"path/filepath"
	"testing"
)

// open поднимает базу во временном файле: она создаётся с нуля,
// наполняется начальными данными и исчезает вместе с каталогом теста.
func open(t *testing.T) *Store {
	t.Helper()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "animals.db"))
	if err != nil {
		t.Fatalf("база не открылась: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesAndSeeds(t *testing.T) {
	s := open(t)

	n, err := s.Count(t.Context())
	if err != nil {
		t.Fatalf("подсчёт: %v", err)
	}
	seed, err := SeedAnimals()
	if err != nil {
		t.Fatalf("начальные данные: %v", err)
	}
	if n != len(seed) {
		t.Fatalf("в базе %d записей, в начальных данных %d", n, len(seed))
	}

	classes, err := s.Classes(t.Context())
	if err != nil || len(classes) == 0 {
		t.Fatalf("классы: %v, %v", classes, err)
	}
}

// Уже наполненную базу повторное открытие трогать не должно: иначе
// удалённое животное воскресало бы при каждом запуске сервера.
func TestReopenKeepsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "animals.db")

	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("база не открылась: %v", err)
	}
	if _, err := first.Delete(t.Context(), "lynx"); err != nil {
		t.Fatalf("удаление: %v", err)
	}
	before, _ := first.Count(t.Context())
	first.Close()

	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("база не переоткрылась: %v", err)
	}
	defer second.Close()

	after, err := second.Count(t.Context())
	if err != nil {
		t.Fatalf("подсчёт: %v", err)
	}
	if after != before {
		t.Errorf("после переоткрытия записей %d, было %d", after, before)
	}
	if _, ok, _ := second.Get(t.Context(), "lynx"); ok {
		t.Error("удалённая запись вернулась при переоткрытии")
	}
}

func TestGetByIDAndNames(t *testing.T) {
	s := open(t)

	for _, key := range []string{"lynx", "Обыкновенная рысь", "обыкновенная рысь", "Lynx lynx"} {
		a, ok, err := s.Get(t.Context(), key)
		if err != nil {
			t.Fatalf("по ключу %q ошибка: %v", key, err)
		}
		if !ok {
			t.Fatalf("по ключу %q запись не нашлась", key)
		}
		if a.ID != "lynx" {
			t.Errorf("по ключу %q нашлось %q", key, a.ID)
		}
		if len(a.Habitats) == 0 || len(a.Food) == 0 {
			t.Errorf("списки не загрузились: %+v", a)
		}
	}

	if _, ok, _ := s.Get(t.Context(), "малая выхухоль"); ok {
		t.Error("нашлось животное, которого в справочнике нет")
	}
	if _, ok, _ := s.Get(t.Context(), "   "); ok {
		t.Error("пустой запрос не должен ничего находить")
	}
}

func TestFindByClassAndDiet(t *testing.T) {
	s := open(t)

	birds, err := s.Find(t.Context(), Filter{Class: "птицы"})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(birds) == 0 {
		t.Fatal("птиц не нашлось")
	}
	for _, a := range birds {
		if a.Class != "птицы" {
			t.Errorf("%s: класс %q", a.ID, a.Class)
		}
	}

	predators, err := s.Find(t.Context(), Filter{Class: "птицы", Diet: "хищник"})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(predators) == 0 || len(predators) > len(birds) {
		t.Fatalf("хищных птиц %d при %d птицах", len(predators), len(birds))
	}
}

// Регистр кириллицы SQLite сам не приводит: если нормализованные
// колонки перестанут заполняться, этот тест первым это заметит.
func TestFindIsCaseInsensitiveForCyrillic(t *testing.T) {
	s := open(t)

	lower, err := s.Find(t.Context(), Filter{Class: "птицы"})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	upper, err := s.Find(t.Context(), Filter{Class: "ПТИЦЫ"})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(lower) != len(upper) {
		t.Errorf("«птицы» дало %d записей, «ПТИЦЫ» — %d", len(lower), len(upper))
	}
}

func TestFindByHabitatSubstring(t *testing.T) {
	s := open(t)

	// «лес» должен находить и «смешанный лес», и «лиственный лес».
	forest, err := s.Find(t.Context(), Filter{Habitat: "лес"})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
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

	// Регион ищется тем же полем: человек не обязан знать наше деление.
	siberia, err := s.Find(t.Context(), Filter{Habitat: "Сибирь"})
	if err != nil || len(siberia) == 0 {
		t.Errorf("по региону «Сибирь» нашлось %d записей, ошибка %v", len(siberia), err)
	}
}

func TestFindWeightRangeOverlap(t *testing.T) {
	s := open(t)
	min, max := 25.0, 20.0

	heavy, err := s.Find(t.Context(), Filter{Query: "рысь", MinWeightKg: &min})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(heavy) != 1 || heavy[0].ID != "lynx" {
		t.Errorf("рысь не прошла фильтр «тяжелее 25 кг»: %v", heavy)
	}

	light, err := s.Find(t.Context(), Filter{Query: "рысь", MaxWeightKg: &max})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	if len(light) != 1 || light[0].ID != "lynx" {
		t.Errorf("рысь не прошла фильтр «легче 20 кг»: %v", light)
	}

	tiny := 0.5
	if got, _ := s.Find(t.Context(), Filter{Query: "рысь", MaxWeightKg: &tiny}); len(got) != 0 {
		t.Errorf("рысь прошла фильтр «легче 0.5 кг»: %v", got)
	}
}

func TestFindPagination(t *testing.T) {
	s := open(t)

	total, err := s.CountFiltered(t.Context(), Filter{})
	if err != nil {
		t.Fatalf("подсчёт: %v", err)
	}
	first, err := s.Find(t.Context(), Filter{Limit: 3})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}
	second, err := s.Find(t.Context(), Filter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("поиск: %v", err)
	}

	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("страницы по %d и %d записей", len(first), len(second))
	}
	if first[0].ID == second[0].ID {
		t.Error("вторая страница повторяет первую")
	}
	// Предел выборки не должен влиять на общее число найденного.
	if n, _ := s.CountFiltered(t.Context(), Filter{Limit: 3}); n != total {
		t.Errorf("с пределом насчитали %d записей, всего %d", n, total)
	}
}

func TestAddUpdateDelete(t *testing.T) {
	s := open(t)
	ctx := t.Context()

	newcomer := Animal{
		ID:             "platypus",
		Name:           "Утконос",
		ScientificName: "Ornithorhynchus anatinus",
		Class:          "млекопитающие",
		Order:          "однопроходные",
		Family:         "утконосовые",
		Diet:           "хищник",
		Habitats:       []string{"река", "пресный водоём"},
		Regions:        []string{"Австралия", "Тасмания"},
		Food:           []string{"личинки", "черви", "рачки"},
		WeightKg:       Range{0.7, 2.4},
		LengthCm:       Range{30, 45},
		LifespanYears:  17,
		Conservation:   "NT",
		Description:    "Яйцекладущее млекопитающее с клювом и ядовитой шпорой.",
	}

	if err := s.Add(ctx, newcomer); err != nil {
		t.Fatalf("добавление: %v", err)
	}

	saved, ok, err := s.Get(ctx, "Ornithorhynchus anatinus")
	if err != nil || !ok {
		t.Fatalf("новая запись не нашлась: ok=%v, err=%v", ok, err)
	}
	if saved.Name != newcomer.Name || len(saved.Food) != 3 || saved.WeightKg != newcomer.WeightKg {
		t.Errorf("запись сохранилась не полностью: %+v", saved)
	}

	// Повторное добавление не должно молча переписывать карточку.
	if err := s.Add(ctx, newcomer); !errors.Is(err, ErrExists) {
		t.Errorf("повторное добавление вернуло %v, ожидалось ErrExists", err)
	}

	status, lifespan := "VU", 20
	updated, err := s.Update(ctx, "platypus", Patch{
		Conservation:  &status,
		LifespanYears: &lifespan,
		Food:          &[]string{"личинки"},
	})
	if err != nil {
		t.Fatalf("изменение: %v", err)
	}
	if updated.Conservation != "VU" || updated.LifespanYears != 20 {
		t.Errorf("изменения не применились: %+v", updated)
	}
	if len(updated.Food) != 1 {
		t.Errorf("список заменился не целиком: %v", updated.Food)
	}
	// Непереданные поля должны остаться прежними.
	if updated.Name != newcomer.Name || len(updated.Habitats) != 2 {
		t.Errorf("изменение задело чужие поля: %+v", updated)
	}

	if _, err := s.Update(ctx, "platypus", Patch{}); err == nil {
		t.Error("пустое изменение прошло без ошибки")
	}
	if _, err := s.Update(ctx, "выдумка", Patch{Name: &status}); !errors.Is(err, ErrNotFound) {
		t.Errorf("изменение несуществующей записи вернуло %v", err)
	}

	deleted, err := s.Delete(ctx, "platypus")
	if err != nil {
		t.Fatalf("удаление: %v", err)
	}
	if deleted.ID != "platypus" {
		t.Errorf("удалили не то: %+v", deleted)
	}
	if _, ok, _ := s.Get(ctx, "platypus"); ok {
		t.Error("запись осталась после удаления")
	}
	if _, err := s.Delete(ctx, "platypus"); !errors.Is(err, ErrNotFound) {
		t.Errorf("повторное удаление вернуло %v", err)
	}
}

// Списки принадлежат карточке: после удаления животного от них не
// должно остаться строк, иначе следующая запись с тем же id получит
// чужую среду обитания.
func TestDeleteRemovesLists(t *testing.T) {
	s := open(t)
	ctx := t.Context()

	if _, err := s.Delete(ctx, "lynx"); err != nil {
		t.Fatalf("удаление: %v", err)
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM habitats WHERE animal_id = 'lynx'`).Scan(&n)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if n != 0 {
		t.Errorf("после удаления осталось %d строк в habitats", n)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]Animal{
		"без id":           {Name: "Кто-то", ScientificName: "Aliquis", Class: "млекопитающие"},
		"без названия":     {ID: "x", ScientificName: "Aliquis", Class: "млекопитающие"},
		"без латыни":       {ID: "x", Name: "Кто-то", Class: "млекопитающие"},
		"без класса":       {ID: "x", Name: "Кто-то", ScientificName: "Aliquis"},
		"вес наоборот":     {ID: "x", Name: "Кто-то", ScientificName: "Aliquis", Class: "птицы", WeightKg: Range{10, 1}},
		"отрицательный":    {ID: "x", Name: "Кто-то", ScientificName: "Aliquis", Class: "птицы", LengthCm: Range{-1, 5}},
		"минус к возрасту": {ID: "x", Name: "Кто-то", ScientificName: "Aliquis", Class: "птицы", LifespanYears: -3},
	}
	for name, a := range cases {
		if err := a.Validate(); err == nil {
			t.Errorf("%s: проверка пропустила запись", name)
		}
	}

	ok := Animal{ID: "x", Name: "Кто-то", ScientificName: "Aliquis", Class: "птицы", WeightKg: Range{1, 2}}
	if err := ok.Validate(); err != nil {
		t.Errorf("годная запись отклонена: %v", err)
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

func TestFieldKnowsAllDeclaredFields(t *testing.T) {
	s := open(t)

	a, ok, err := s.Get(t.Context(), "lynx")
	if err != nil || !ok {
		t.Fatalf("рысь не нашлась: %v", err)
	}
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

func TestRandomReturnsCatalogEntry(t *testing.T) {
	s := open(t)

	for range 10 {
		a, err := s.Random(t.Context())
		if err != nil {
			t.Fatalf("случайная запись: %v", err)
		}
		if _, ok, _ := s.Get(t.Context(), a.ID); !ok {
			t.Fatalf("случайная запись %q не из справочника", a.ID)
		}
	}
}
