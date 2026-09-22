package catalog

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // драйвер SQLite на чистом Go, без cgo
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound — записи с таким идентификатором нет.
var ErrNotFound = errors.New("животного нет в справочнике")

// ErrExists — запись с таким идентификатором уже есть.
var ErrExists = errors.New("животное с таким идентификатором уже есть")

// MemoryPath — путь для базы в памяти: живёт, пока открыто соединение.
// Общий кэш обязателен, иначе каждое соединение пула получит свою
// пустую базу.
const MemoryPath = "file::memory:?cache=shared"

// Store — справочник в SQLite.
type Store struct {
	db   *sql.DB
	path string
}

// Open открывает базу, создаёт схему, если её нет, и наполняет пустую
// базу начальными данными. Уже наполненную базу не трогает: удалённое
// животное не должно возвращаться при следующем запуске.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("путь к базе пуст")
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("база %s не открылась: %w", path, err)
	}
	// SQLite — файл, а не сервер: пишущее соединение всё равно одно.
	// Ограничение пула избавляет от «database is locked» на записи и
	// заодно делает осмысленной базу в памяти.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("база %s не отвечает: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("схема не создалась: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.seedIfEmpty(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// dsn собирает строку подключения. Режим WAL и ожидание блокировки —
// обычная гигиена для базы, в которую пишут.
func dsn(path string) string {
	if strings.HasPrefix(path, "file:") {
		return path
	}
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
}

func (s *Store) Close() error { return s.db.Close() }

// Path — путь к файлу базы, как его назвали при открытии.
func (s *Store) Path() string { return s.path }

// Count — число записей.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM animals`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("подсчёт записей: %w", err)
	}
	return n, nil
}

// Classes, Diets, Habitats — значения, которые встречаются в базе.
// Клиенту они нужны, чтобы знать, чем осмысленно фильтровать.
func (s *Store) Classes(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `SELECT DISTINCT class FROM animals WHERE class <> '' ORDER BY class`)
}

func (s *Store) Diets(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `SELECT DISTINCT diet FROM animals WHERE diet <> '' ORDER BY diet`)
}

func (s *Store) Habitats(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `SELECT DISTINCT value FROM habitats ORDER BY value`)
}

func (s *Store) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("запрос: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("чтение строки: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get ищет животное по идентификатору, а если такого нет — по русскому
// или латинскому названию. Модель одинаково охотно присылает и то и
// другое, и отказывать из-за формы имени незачем.
func (s *Store) Get(ctx context.Context, idOrName string) (Animal, bool, error) {
	key := norm(idOrName)
	if key == "" {
		return Animal{}, false, nil
	}

	animals, err := s.query(ctx,
		`WHERE id = ? OR name_norm = ? OR scientific_norm = ? ORDER BY position LIMIT 1`,
		key, key, key)
	if err != nil {
		return Animal{}, false, err
	}
	if len(animals) == 0 {
		return Animal{}, false, nil
	}
	return animals[0], true, nil
}

// Find отбирает записи по фильтру в порядке справочника.
func (s *Store) Find(ctx context.Context, f Filter) ([]Animal, error) {
	where, args := f.condition()
	tail := where + ` ORDER BY position`
	if f.Limit > 0 {
		tail += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, max(f.Offset, 0))
	}
	return s.query(ctx, tail, args...)
}

// CountFiltered — сколько записей подошло бы под фильтр без учёта
// постраничной выдачи: клиенту нужно знать, сколько всего нашлось.
func (s *Store) CountFiltered(ctx context.Context, f Filter) (int, error) {
	where, args := f.condition()

	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM animals `+where, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("подсчёт по фильтру: %w", err)
	}
	return n, nil
}

// condition собирает WHERE. Строковые поля сравниваются по вхождению
// без учёта регистра — по нормализованным колонкам, потому что LIKE в
// SQLite приводит регистр только у латиницы.
func (f Filter) condition() (string, []any) {
	var (
		conds []string
		args  []any
	)

	if v := norm(f.Class); v != "" {
		conds = append(conds, `class_norm LIKE ?`)
		args = append(args, "%"+v+"%")
	}
	if v := norm(f.Diet); v != "" {
		conds = append(conds, `diet_norm LIKE ?`)
		args = append(args, "%"+v+"%")
	}
	if v := norm(f.Habitat); v != "" {
		// Среда обитания и регион для запроса — одно и то же: человек
		// пишет «тайга» или «Сибирь», не думая о нашем делении.
		conds = append(conds, `(EXISTS (SELECT 1 FROM habitats h WHERE h.animal_id = animals.id AND h.value_norm LIKE ?)
			OR EXISTS (SELECT 1 FROM regions r WHERE r.animal_id = animals.id AND r.value_norm LIKE ?))`)
		args = append(args, "%"+v+"%", "%"+v+"%")
	}
	if v := norm(f.Query); v != "" {
		conds = append(conds, `(id LIKE ? OR name_norm LIKE ? OR scientific_norm LIKE ? OR description_norm LIKE ?
			OR EXISTS (SELECT 1 FROM habitats h WHERE h.animal_id = animals.id AND h.value_norm LIKE ?)
			OR EXISTS (SELECT 1 FROM regions r WHERE r.animal_id = animals.id AND r.value_norm LIKE ?)
			OR EXISTS (SELECT 1 FROM food fd WHERE fd.animal_id = animals.id AND fd.value_norm LIKE ?))`)
		like := "%" + v + "%"
		args = append(args, like, like, like, like, like, like, like)
	}
	// Вес сравнивается по пересечению диапазонов, а не по середине:
	// рысь весом 18–30 кг подходит и под «тяжелее 25», и под «легче 20».
	if f.MinWeightKg != nil {
		conds = append(conds, `weight_max_kg >= ?`)
		args = append(args, *f.MinWeightKg)
	}
	if f.MaxWeightKg != nil {
		conds = append(conds, `weight_min_kg <= ?`)
		args = append(args, *f.MaxWeightKg)
	}

	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// Random возвращает случайную запись.
func (s *Store) Random(ctx context.Context) (Animal, error) {
	ids, err := s.strings(ctx, `SELECT id FROM animals ORDER BY position`)
	if err != nil {
		return Animal{}, err
	}
	if len(ids) == 0 {
		return Animal{}, ErrNotFound
	}

	// Случайность берём в Go, а не через ORDER BY random(): так её
	// видно в коде и не приходится доверять порядку строк.
	a, ok, err := s.Get(ctx, ids[rand.IntN(len(ids))])
	if err != nil {
		return Animal{}, err
	}
	if !ok {
		return Animal{}, ErrNotFound
	}
	return a, nil
}

// query — единственное место, где читаются карточки: сначала сами
// записи, затем их списки одним запросом на таблицу. Джойнить всё
// вместе нельзя — три списка размножили бы строки друг на друга.
func (s *Store) query(ctx context.Context, tail string, args ...any) ([]Animal, error) {
	const columns = `SELECT id, name, scientific_name, class, "order", family, diet,
		weight_min_kg, weight_max_kg, length_min_cm, length_max_cm,
		lifespan_years, conservation_status, description FROM animals `

	rows, err := s.db.QueryContext(ctx, columns+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("выборка животных: %w", err)
	}
	defer rows.Close()

	var (
		animals []Animal
		byID    = map[string]int{}
		ids     []any
	)
	for rows.Next() {
		var a Animal
		err := rows.Scan(&a.ID, &a.Name, &a.ScientificName, &a.Class, &a.Order, &a.Family, &a.Diet,
			&a.WeightKg.Min, &a.WeightKg.Max, &a.LengthCm.Min, &a.LengthCm.Max,
			&a.LifespanYears, &a.Conservation, &a.Description)
		if err != nil {
			return nil, fmt.Errorf("чтение карточки: %w", err)
		}
		a.Habitats, a.Regions, a.Food = []string{}, []string{}, []string{}
		byID[a.ID] = len(animals)
		ids = append(ids, a.ID)
		animals = append(animals, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(animals) == 0 {
		return []Animal{}, nil
	}

	lists := []struct {
		table string
		field func(*Animal) *[]string
	}{
		{"habitats", func(a *Animal) *[]string { return &a.Habitats }},
		{"regions", func(a *Animal) *[]string { return &a.Regions }},
		{"food", func(a *Animal) *[]string { return &a.Food }},
	}
	for _, list := range lists {
		q := fmt.Sprintf(
			`SELECT animal_id, value FROM %s WHERE animal_id IN (%s) ORDER BY animal_id, position`,
			list.table, placeholders(len(ids)))

		listRows, err := s.db.QueryContext(ctx, q, ids...)
		if err != nil {
			return nil, fmt.Errorf("выборка из %s: %w", list.table, err)
		}
		for listRows.Next() {
			var id, value string
			if err := listRows.Scan(&id, &value); err != nil {
				listRows.Close()
				return nil, fmt.Errorf("чтение %s: %w", list.table, err)
			}
			if i, ok := byID[id]; ok {
				field := list.field(&animals[i])
				*field = append(*field, value)
			}
		}
		err = errors.Join(listRows.Err(), listRows.Close())
		if err != nil {
			return nil, err
		}
	}
	return animals, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Add добавляет запись. Идентификатор занят — ошибка, а не молчаливая
// замена: подменить чужую карточку опечаткой в id слишком легко.
func (s *Store) Add(ctx context.Context, a Animal) error {
	if err := a.Validate(); err != nil {
		return err
	}
	a.ID = norm(a.ID)

	if _, ok, err := s.Get(ctx, a.ID); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%w: %s", ErrExists, a.ID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("начало транзакции: %w", err)
	}
	defer tx.Rollback()

	var position int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(position), 0) + 1 FROM animals`).Scan(&position); err != nil {
		return fmt.Errorf("порядковый номер: %w", err)
	}
	if err := insert(ctx, tx, a, position); err != nil {
		return err
	}
	return tx.Commit()
}

// Update меняет поля записи и возвращает её новое состояние.
func (s *Store) Update(ctx context.Context, id string, p Patch) (Animal, error) {
	if p.Empty() {
		return Animal{}, fmt.Errorf("не переданы поля для изменения")
	}

	current, ok, err := s.Get(ctx, id)
	if err != nil {
		return Animal{}, err
	}
	if !ok {
		return Animal{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	updated := p.Apply(current)
	updated.ID = current.ID
	if err := updated.Validate(); err != nil {
		return Animal{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Animal{}, fmt.Errorf("начало транзакции: %w", err)
	}
	defer tx.Rollback()

	var position int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM animals WHERE id = ?`, current.ID).Scan(&position); err != nil {
		return Animal{}, fmt.Errorf("порядковый номер: %w", err)
	}
	// Запись переписывается целиком на прежнем месте: полей много, а
	// собирать динамический UPDATE ради экономии одного оператора —
	// плохой размен на читаемость.
	if _, err := tx.ExecContext(ctx, `DELETE FROM animals WHERE id = ?`, current.ID); err != nil {
		return Animal{}, fmt.Errorf("удаление старой версии: %w", err)
	}
	if err := insert(ctx, tx, updated, position); err != nil {
		return Animal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Animal{}, fmt.Errorf("запись изменений: %w", err)
	}
	return updated, nil
}

// Delete удаляет запись и возвращает её напоследок: клиент должен
// видеть, что именно исчезло.
func (s *Store) Delete(ctx context.Context, id string) (Animal, error) {
	a, ok, err := s.Get(ctx, id)
	if err != nil {
		return Animal{}, err
	}
	if !ok {
		return Animal{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM animals WHERE id = ?`, a.ID); err != nil {
		return Animal{}, fmt.Errorf("удаление: %w", err)
	}
	return a, nil
}

// insert пишет карточку и её списки. Списковые таблицы связаны с
// animals внешним ключом с ON DELETE CASCADE, так что чистить их перед
// перезаписью не нужно — достаточно удалить саму карточку.
func insert(ctx context.Context, tx *sql.Tx, a Animal, position int) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO animals (id, name, name_norm, scientific_name, scientific_norm,
			class, class_norm, "order", family, diet, diet_norm,
			weight_min_kg, weight_max_kg, length_min_cm, length_max_cm,
			lifespan_years, conservation_status, description, description_norm, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm(a.ID), a.Name, norm(a.Name), a.ScientificName, norm(a.ScientificName),
		a.Class, norm(a.Class), a.Order, a.Family, a.Diet, norm(a.Diet),
		a.WeightKg.Min, a.WeightKg.Max, a.LengthCm.Min, a.LengthCm.Max,
		a.LifespanYears, a.Conservation, a.Description, norm(a.Description), position)
	if err != nil {
		return fmt.Errorf("запись карточки %s: %w", a.ID, err)
	}

	lists := map[string][]string{
		"habitats": a.Habitats,
		"regions":  a.Regions,
		"food":     a.Food,
	}
	for table, values := range lists {
		for i, v := range values {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			_, err := tx.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO %s (animal_id, value, value_norm, position) VALUES (?, ?, ?, ?)`, table),
				norm(a.ID), v, norm(v), i)
			if err != nil {
				return fmt.Errorf("запись в %s для %s: %w", table, a.ID, err)
			}
		}
	}
	return nil
}
