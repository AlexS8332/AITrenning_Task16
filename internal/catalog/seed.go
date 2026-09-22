package catalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
)

// animalsJSON — начальные данные справочника. Лежат текстом рядом с
// кодом: так их видно в истории изменений, а база остаётся файлом,
// который можно удалить и получить заново.
//
//go:embed animals.json
var animalsJSON []byte

// SeedAnimals разбирает начальные данные.
func SeedAnimals() ([]Animal, error) {
	var animals []Animal
	if err := json.Unmarshal(animalsJSON, &animals); err != nil {
		return nil, fmt.Errorf("начальные данные не разобрались: %w", err)
	}
	if len(animals) == 0 {
		return nil, fmt.Errorf("начальные данные пусты")
	}

	seen := make(map[string]bool, len(animals))
	for _, a := range animals {
		if err := a.Validate(); err != nil {
			return nil, fmt.Errorf("начальные данные, запись %q: %w", a.ID, err)
		}
		if seen[norm(a.ID)] {
			return nil, fmt.Errorf("начальные данные: повторяющийся id %q", a.ID)
		}
		seen[norm(a.ID)] = true
	}
	return animals, nil
}

// seedIfEmpty наполняет базу, если в ней ещё нет записей. Именно «если
// пуста», а не «при создании файла»: удалённое через delete_animal
// животное не должно воскресать при следующем запуске сервера, а вот
// потерянную базу сервер восстановит сам.
func (s *Store) seedIfEmpty(ctx context.Context) error {
	n, err := s.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	animals, err := SeedAnimals()
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("начало транзакции: %w", err)
	}
	defer tx.Rollback()

	for i, a := range animals {
		if err := insert(ctx, tx, a, i+1); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("наполнение базы: %w", err)
	}
	return nil
}
