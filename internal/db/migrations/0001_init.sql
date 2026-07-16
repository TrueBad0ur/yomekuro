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
    format            TEXT NOT NULL DEFAULT 'epub',
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

-- Manga-upload → GPU-conversion job queue. converter-worker claims rows via
-- `SELECT ... WHERE status='pending' FOR UPDATE SKIP LOCKED` so multiple
-- worker instances could poll concurrently without double-processing.
CREATE TABLE conversion_jobs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    input_path  TEXT NOT NULL,
    output_path TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- pending | running | done | failed | stopped
    error       TEXT NOT NULL DEFAULT '',
    current_volume TEXT NOT NULL DEFAULT '',
    stop_requested BOOLEAN NOT NULL DEFAULT false,
    force_ocr   BOOLEAN NOT NULL DEFAULT false, -- reconvert: full OCR re-run, ignoring mokuro's cache
    volume      TEXT NOT NULL DEFAULT '', -- reconvert: limit to one volume; '' means the whole book
    detector_size INTEGER NOT NULL DEFAULT 3072, -- text-detector input resolution (px); higher = slower, fewer merged-line misreads
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversion_jobs_status ON conversion_jobs(status);

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
