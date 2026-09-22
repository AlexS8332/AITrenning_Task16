package mcpserver

import (
	"context"
	"errors"
	"strings"

	"github.com/AlexS8332/AITrenning_Task16/internal/catalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Инструменты, которые меняют справочник. В аннотациях у них
// readOnlyHint=false, а у удаления ещё и destructiveHint: по протоколу
// это подсказка клиенту, что такой вызов стоит показать человеку и
// спросить разрешения, прежде чем выполнять.

// AddIn — аргументы add_animal. Поля повторяют карточку; обязательны
// только те, без которых запись бессмысленна.
type AddIn struct {
	ID             string         `json:"id" jsonschema:"идентификатор латиницей через дефис, например snow-leopard"`
	Name           string         `json:"name" jsonschema:"русское название"`
	ScientificName string         `json:"scientific_name" jsonschema:"латинское (научное) название"`
	Class          string         `json:"class" jsonschema:"класс: млекопитающие, птицы, рыбы и так далее"`
	Order          string         `json:"order,omitempty" jsonschema:"отряд"`
	Family         string         `json:"family,omitempty" jsonschema:"семейство"`
	Diet           string         `json:"diet,omitempty" jsonschema:"тип питания: хищник, травоядное, всеядное, насекомоядное"`
	Habitats       []string       `json:"habitats,omitempty" jsonschema:"среды обитания"`
	Regions        []string       `json:"regions,omitempty" jsonschema:"регионы распространения"`
	Food           []string       `json:"food,omitempty" jsonschema:"чем питается"`
	WeightKg       *catalog.Range `json:"weight_kg,omitempty" jsonschema:"вес в килограммах, от и до"`
	LengthCm       *catalog.Range `json:"length_cm,omitempty" jsonschema:"длина тела в сантиметрах, от и до"`
	LifespanYears  int            `json:"lifespan_years,omitempty" jsonschema:"продолжительность жизни в годах"`
	Conservation   string         `json:"conservation_status,omitempty" jsonschema:"охранный статус МСОП: LC, NT, VU, EN, CR, DD"`
	Description    string         `json:"description,omitempty" jsonschema:"короткое описание"`
}

func (in AddIn) animal() catalog.Animal {
	a := catalog.Animal{
		ID:             strings.TrimSpace(in.ID),
		Name:           strings.TrimSpace(in.Name),
		ScientificName: strings.TrimSpace(in.ScientificName),
		Class:          strings.TrimSpace(in.Class),
		Order:          strings.TrimSpace(in.Order),
		Family:         strings.TrimSpace(in.Family),
		Diet:           strings.TrimSpace(in.Diet),
		Habitats:       in.Habitats,
		Regions:        in.Regions,
		Food:           in.Food,
		LifespanYears:  in.LifespanYears,
		Conservation:   strings.TrimSpace(in.Conservation),
		Description:    strings.TrimSpace(in.Description),
	}
	if in.WeightKg != nil {
		a.WeightKg = *in.WeightKg
	}
	if in.LengthCm != nil {
		a.LengthCm = *in.LengthCm
	}
	return a
}

// UpdateIn — аргументы update_animal. Все поля, кроме animal, —
// указатели: только так «не трогать поле» отличается от «записать в
// поле пустоту».
type UpdateIn struct {
	Animal         string         `json:"animal" jsonschema:"какую запись менять: идентификатор или название"`
	Name           *string        `json:"name,omitempty" jsonschema:"русское название"`
	ScientificName *string        `json:"scientific_name,omitempty" jsonschema:"латинское название"`
	Class          *string        `json:"class,omitempty" jsonschema:"класс"`
	Order          *string        `json:"order,omitempty" jsonschema:"отряд"`
	Family         *string        `json:"family,omitempty" jsonschema:"семейство"`
	Diet           *string        `json:"diet,omitempty" jsonschema:"тип питания"`
	Habitats       *[]string      `json:"habitats,omitempty" jsonschema:"среды обитания; список заменяется целиком"`
	Regions        *[]string      `json:"regions,omitempty" jsonschema:"регионы; список заменяется целиком"`
	Food           *[]string      `json:"food,omitempty" jsonschema:"чем питается; список заменяется целиком"`
	WeightKg       *catalog.Range `json:"weight_kg,omitempty" jsonschema:"вес в килограммах, от и до"`
	LengthCm       *catalog.Range `json:"length_cm,omitempty" jsonschema:"длина тела в сантиметрах, от и до"`
	LifespanYears  *int           `json:"lifespan_years,omitempty" jsonschema:"продолжительность жизни в годах"`
	Conservation   *string        `json:"conservation_status,omitempty" jsonschema:"охранный статус МСОП"`
	Description    *string        `json:"description,omitempty" jsonschema:"короткое описание"`
}

func (in UpdateIn) patch() catalog.Patch {
	return catalog.Patch{
		Name:           in.Name,
		ScientificName: in.ScientificName,
		Class:          in.Class,
		Order:          in.Order,
		Family:         in.Family,
		Diet:           in.Diet,
		Habitats:       in.Habitats,
		Regions:        in.Regions,
		Food:           in.Food,
		WeightKg:       in.WeightKg,
		LengthCm:       in.LengthCm,
		LifespanYears:  in.LifespanYears,
		Conservation:   in.Conservation,
		Description:    in.Description,
	}
}

// DeleteIn — аргументы delete_animal.
type DeleteIn struct {
	Animal string `json:"animal" jsonschema:"идентификатор или название удаляемой записи"`
}

// WriteOut — общий результат изменяющих инструментов: что произошло и с
// какой записью. Карточка возвращается и при удалении — клиент должен
// видеть, что именно исчезло.
type WriteOut struct {
	Done    string          `json:"done" jsonschema:"что сделано: added, updated или deleted"`
	Animal  *catalog.Animal `json:"animal,omitempty" jsonschema:"карточка после изменения (для удаления — какой она была)"`
	Animals int             `json:"animals" jsonschema:"сколько записей в справочнике осталось"`
}

func (s *Server) addWriteTools() {
	addTool(s, &mcp.Tool{
		Name:  "add_animal",
		Title: "Добавить животное",
		Description: "Добавляет запись в справочник. Идентификатор должен быть свободен: " +
			"если он занят, вызов отклоняется, а не переписывает чужую карточку. " +
			"Изменяет базу данных.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false},
	}, s.addAnimal)

	addTool(s, &mcp.Tool{
		Name:  "update_animal",
		Title: "Изменить животное",
		Description: "Меняет поля записи. Переданные поля заменяют прежние значения, " +
			"остальные остаются как были; списки (habitats, regions, food) заменяются целиком. " +
			"Изменяет базу данных.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: true},
	}, s.updateAnimal)

	addTool(s, &mcp.Tool{
		Name:  "delete_animal",
		Title: "Удалить животное",
		Description: "Удаляет запись из справочника и возвращает её напоследок. " +
			"Отменить нельзя: восстановить запись можно только добавив заново. " +
			"Изменяет базу данных.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			IdempotentHint:  true,
			DestructiveHint: ptr(true),
		},
	}, s.deleteAnimal)
}

func (s *Server) addAnimal(ctx context.Context, req *mcp.CallToolRequest, in AddIn) (*mcp.CallToolResult, WriteOut, error) {
	animal := in.animal()
	if err := s.cat.Add(ctx, animal); err != nil {
		if errors.Is(err, catalog.ErrExists) {
			return nil, WriteOut{}, errf("%v; чтобы изменить существующую запись, вызовите update_animal", err)
		}
		return nil, WriteOut{}, err
	}

	// Перечитываем запись из базы, а не отдаём то, что прислали: так
	// видно, что именно сохранилось.
	saved, _, err := s.cat.Get(ctx, animal.ID)
	if err != nil {
		return nil, WriteOut{}, err
	}
	out, err := s.writeOut(ctx, "added", &saved)
	return nil, out, err
}

func (s *Server) updateAnimal(ctx context.Context, req *mcp.CallToolRequest, in UpdateIn) (*mcp.CallToolResult, WriteOut, error) {
	if strings.TrimSpace(in.Animal) == "" {
		return nil, WriteOut{}, errf("animal пуст: укажите, какую запись менять")
	}

	updated, err := s.cat.Update(ctx, in.Animal, in.patch())
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, WriteOut{}, errf("%v; список записей даёт list_animals", err)
		}
		return nil, WriteOut{}, err
	}
	out, err := s.writeOut(ctx, "updated", &updated)
	return nil, out, err
}

func (s *Server) deleteAnimal(ctx context.Context, req *mcp.CallToolRequest, in DeleteIn) (*mcp.CallToolResult, WriteOut, error) {
	if strings.TrimSpace(in.Animal) == "" {
		return nil, WriteOut{}, errf("animal пуст: укажите, какую запись удалять")
	}

	deleted, err := s.cat.Delete(ctx, in.Animal)
	if err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil, WriteOut{}, errf("%v; удалять нечего", err)
		}
		return nil, WriteOut{}, err
	}
	out, err := s.writeOut(ctx, "deleted", &deleted)
	return nil, out, err
}

func (s *Server) writeOut(ctx context.Context, done string, a *catalog.Animal) (WriteOut, error) {
	n, err := s.cat.Count(ctx)
	if err != nil {
		return WriteOut{}, err
	}
	return WriteOut{Done: done, Animal: a, Animals: n}, nil
}
