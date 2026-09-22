-- Схема справочника. Списки (среда обитания, регионы, пища) вынесены в
-- отдельные таблицы: по ним идёт поиск, а значит они должны быть
-- строками, а не полем со всем содержимым через запятую.
--
-- Рядом с каждым текстовым полем, по которому ищут, лежит колонка
-- *_norm — то же значение в нижнем регистре. Так приходится делать
-- потому, что LIKE и lower() в SQLite приводят регистр только у
-- латиницы: «Тайга» и «тайга» для базы разные слова, а нормализацию в
-- Go делает strings.ToLower, который знает про Unicode.

CREATE TABLE IF NOT EXISTS animals (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    name_norm           TEXT NOT NULL,
    scientific_name     TEXT NOT NULL,
    scientific_norm     TEXT NOT NULL,
    class               TEXT NOT NULL,
    class_norm          TEXT NOT NULL,
    "order"             TEXT NOT NULL DEFAULT '',
    family              TEXT NOT NULL DEFAULT '',
    diet                TEXT NOT NULL DEFAULT '',
    diet_norm           TEXT NOT NULL DEFAULT '',
    weight_min_kg       REAL NOT NULL DEFAULT 0,
    weight_max_kg       REAL NOT NULL DEFAULT 0,
    length_min_cm       REAL NOT NULL DEFAULT 0,
    length_max_cm       REAL NOT NULL DEFAULT 0,
    lifespan_years      INTEGER NOT NULL DEFAULT 0,
    conservation_status TEXT NOT NULL DEFAULT '',
    description         TEXT NOT NULL DEFAULT '',
    description_norm    TEXT NOT NULL DEFAULT '',
    -- Порядок записей в ответах должен быть устойчивым, иначе
    -- постраничная выдача начнёт перемешиваться между вызовами.
    position            INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS animals_class  ON animals (class_norm);
CREATE INDEX IF NOT EXISTS animals_diet   ON animals (diet_norm);
CREATE INDEX IF NOT EXISTS animals_weight ON animals (weight_min_kg, weight_max_kg);

-- Три списковые таблицы устроены одинаково: значение, его нормальная
-- форма и порядок внутри карточки.
CREATE TABLE IF NOT EXISTS habitats (
    animal_id TEXT NOT NULL REFERENCES animals (id) ON DELETE CASCADE,
    value     TEXT NOT NULL,
    value_norm TEXT NOT NULL,
    position  INTEGER NOT NULL,
    PRIMARY KEY (animal_id, position)
);

CREATE TABLE IF NOT EXISTS regions (
    animal_id TEXT NOT NULL REFERENCES animals (id) ON DELETE CASCADE,
    value     TEXT NOT NULL,
    value_norm TEXT NOT NULL,
    position  INTEGER NOT NULL,
    PRIMARY KEY (animal_id, position)
);

CREATE TABLE IF NOT EXISTS food (
    animal_id TEXT NOT NULL REFERENCES animals (id) ON DELETE CASCADE,
    value     TEXT NOT NULL,
    value_norm TEXT NOT NULL,
    position  INTEGER NOT NULL,
    PRIMARY KEY (animal_id, position)
);

CREATE INDEX IF NOT EXISTS habitats_value ON habitats (value_norm);
CREATE INDEX IF NOT EXISTS regions_value  ON regions (value_norm);
CREATE INDEX IF NOT EXISTS food_value     ON food (value_norm);
