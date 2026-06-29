CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE libraries (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    path       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE books (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id        UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path              TEXT NOT NULL UNIQUE,
    filename          TEXT NOT NULL,
    file_size         BIGINT,
    file_hash         TEXT,
    file_modified     TIMESTAMPTZ,
    title             TEXT,
    sort_title        TEXT,
    authors           TEXT[],
    language          TEXT,
    publisher         TEXT,
    published_at      DATE,
    description       TEXT,
    isbn              TEXT,
    series_name       TEXT,
    series_index      DOUBLE PRECISION,
    page_count        INT,
    reading_direction TEXT,
    cover_path        TEXT,
    cover_media_type  TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_books_library    ON books(library_id);
CREATE INDEX idx_books_language   ON books(language);
CREATE INDEX idx_books_series     ON books(series_name, series_index);
CREATE INDEX idx_books_sort_title ON books(sort_title);

CREATE TABLE tags (
    id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE book_tags (
    book_id UUID REFERENCES books(id) ON DELETE CASCADE,
    tag_id  UUID REFERENCES tags(id)  ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin      BOOLEAN NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE reading_progress (
    book_id        UUID NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    spine_index    INT NOT NULL DEFAULT 0,
    progression    DOUBLE PRECISION NOT NULL DEFAULT 0,
    percentage     DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bookmark_spine INT,
    bookmark_elem  INT,
    bookmark_start INT,
    bookmark_end   INT,
    PRIMARY KEY (book_id, user_id)
);
