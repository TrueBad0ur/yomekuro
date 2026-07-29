'use strict';

// ── Auth guard ────────────────────────────────────────────────────────────────

let currentUser = null;

async function checkAuth() {
  const res = await fetch('/api/auth/me');
  if (!res.ok) { location.href = '/login'; return false; }
  currentUser = await res.json();
  return true;
}

document.getElementById('btn-logout').addEventListener('click', async () => {
  await fetch('/api/auth/logout', { method: 'POST' });
  location.href = '/login';
});

// ── Theme ─────────────────────────────────────────────────────────────────────

function applyTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  localStorage.setItem('theme', theme);
  document.querySelectorAll('.theme-opt').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.theme === theme);
  });
}

document.querySelectorAll('.theme-opt').forEach(btn => {
  btn.addEventListener('click', () => applyTheme(btn.dataset.theme));
});

applyTheme(localStorage.getItem('theme') || 'dark');

// ── Image/page cache toggle ──────────────────────────────────────────────────
// 'on' (default): plain URLs, so the browser's HTTP cache (server sends a real
// max-age now, not just an ETag to revalidate) serves repeat views instantly.
// 'off': every image/content URL gets a fresh cache-busting query param, so
// nothing is ever read from cache — for right after a reconvert, when you want
// to see the new pages immediately instead of whatever the browser cached.
function getCacheMode() {
  return localStorage.getItem('cacheMode') || 'on';
}

function cacheBust(url) {
  if (getCacheMode() === 'off') {
    return url + (url.includes('?') ? '&' : '?') + '_nc=' + Date.now();
  }
  return url;
}

function applyCacheMode(mode) {
  localStorage.setItem('cacheMode', mode);
  const btn = document.getElementById('btn-cache-toggle');
  if (btn) btn.textContent = mode === 'off' ? 'Cache: Off' : 'Cache: On';
  if (mode === 'off' && window.caches && caches.keys) {
    caches.keys().then(keys => keys.forEach(k => caches.delete(k))).catch(() => {});
  }
}

const cacheToggleBtn = document.getElementById('btn-cache-toggle');
if (cacheToggleBtn) {
  cacheToggleBtn.addEventListener('click', () => {
    applyCacheMode(getCacheMode() === 'off' ? 'on' : 'off');
    // A full reload is the simplest way to make every already-rendered cover
    // (and reader.js, on its own next load) actually pick up the new mode.
    location.reload();
  });
  applyCacheMode(getCacheMode());
}

// ── State ─────────────────────────────────────────────────────────────────────

let allSeries   = [];
let searchQuery = '';
let activeTag   = '';
let debounceTimer = null;
let currentView = 'personal'; // 'personal' | 'titles' | 'series' | 'tag' | 'library'
let currentLibrary = null;
let currentLibrarySeries = [];
let personalCategories = [];

// ── DOM refs ──────────────────────────────────────────────────────────────────

const grid          = document.getElementById('books-grid');
const emptyMsg      = document.getElementById('empty-msg');
const viewTitle     = document.getElementById('view-title');
const breadcrumb    = document.getElementById('breadcrumb');
const searchInput   = document.getElementById('search-input');
const btnSearch     = document.getElementById('btn-search');
const libraryGroups = document.getElementById('library-groups');
const tagChips      = document.getElementById('tag-chips');
const tagChipsEmpty = document.getElementById('tag-chips-empty');
const navMyLibrary  = document.getElementById('nav-my-library');
const navAllTitles  = document.getElementById('nav-all-titles');
const filterBar     = document.querySelector('.filter-bar');
const personalDashboard = document.getElementById('personal-dashboard');

// ── Browser back/forward ─────────────────────────────────────────────────────
// Every view change (library/series/tag) gets its own history entry, so
// pressing Back steps back through what was actually browsed (series → its
// library → all titles) instead of always dropping to the home page. Search
// input uses 'replace' instead of 'push' so refining a search doesn't leave a
// back-stop per keystroke.

function currentViewParams() {
  const p = new URLSearchParams();
  if (currentView === 'titles') {
    p.set('view', 'titles');
  } else if (currentView === 'library' && currentLibrary) {
    p.set('view', 'library');
    p.set('lib', currentLibrary.id);
    p.set('libname', currentLibrary.name);
  } else if (currentView === 'series') {
    p.set('view', 'series');
    p.set('series', viewTitle.textContent);
  } else if (currentView === 'tag') {
    p.set('view', 'tag');
    p.set('tag', viewTitle.textContent);
  }
  if (searchQuery) p.set('q', searchQuery);
  return p;
}

// historyMode: 'push' (default — a real navigation), 'replace' (refining the
// current view, e.g. search-as-you-type), or 'none' (already reflected in the
// URL — restoring on load, or a popstate event).
function syncAppHistory(historyMode) {
  if (historyMode === 'none') return;
  const params = currentViewParams();
  const url = params.toString() ? `/?${params.toString()}` : '/';
  if (historyMode === 'replace') {
    history.replaceState({ appView: true }, '', url);
  } else {
    history.pushState({ appView: true }, '', url);
  }
}

window.addEventListener('popstate', () => { restoreFromURL(); });

// ── Views ─────────────────────────────────────────────────────────────────────

function showStandardContent() {
  personalDashboard.hidden = true;
  filterBar.hidden = false;
  grid.hidden = false;
}

async function showPersonalLibrary(historyMode) {
  currentView = 'personal';
  currentLibrary = null;
  breadcrumb.hidden = true;
  viewTitle.textContent = 'My Library';
  setActiveNav(navMyLibrary);
  filterBar.hidden = true;
  grid.hidden = true;
  emptyMsg.hidden = true;
  personalDashboard.hidden = false;
  syncAppHistory(historyMode || 'push');

  const stats = document.getElementById('personal-stats');
  const continueGrid = document.getElementById('continue-grid');
  const completedGrid = document.getElementById('completed-grid');
  const personalEmpty = document.getElementById('personal-empty');
  stats.innerHTML = '<div class="personal-loading">Loading your library…</div>';
  continueGrid.innerHTML = '';
  completedGrid.innerHTML = '';
  personalEmpty.hidden = true;

  let items;
  try {
    const [libraryData, categoryData] = await Promise.all([
      fetch('/api/me/library').then(r => {
        if (!r.ok) throw new Error('request failed');
        return r.json();
      }),
      loadPersonalCategories(),
    ]);
    items = libraryData.items || [];
    personalCategories = categoryData;
  } catch {
    stats.innerHTML = '<div class="personal-loading personal-error">Failed to load your library.</div>';
    return;
  }

  const reading = items.filter(item => !item.completed);
  const completed = items.filter(item => item.completed);
  const readVolumes = items.reduce((sum, item) => sum + item.read_count, 0);
  stats.innerHTML = `
    <div class="personal-stat"><strong>${reading.length}</strong><span>Reading</span></div>
    <div class="personal-stat"><strong>${completed.length}</strong><span>Completed series</span></div>
    <div class="personal-stat"><strong>${readVolumes}</strong><span>Volumes read</span></div>`;

  document.getElementById('continue-count').textContent =
    `${reading.length} title${reading.length === 1 ? '' : 's'}`;
  document.getElementById('completed-count').textContent =
    `${completed.length} title${completed.length === 1 ? '' : 's'}`;
  document.getElementById('continue-section').hidden = reading.length === 0;
  document.getElementById('completed-section').hidden = completed.length === 0;
  personalEmpty.hidden = items.length > 0 ||
    personalCategories.some(category => (category.items || []).length > 0);
  renderPersonalCategories();

  for (const item of reading) {
    const pct = Math.round(item.progress_pct * 100);
    const action = item.target_was_started ? 'Continue' : 'Next';
    const card = document.createElement('button');
    card.className = 'personal-card';
    card.innerHTML = `
      <img src="${cacheBust(item.cover_url)}" alt="${esc(item.name)}"
           onerror="this.style.display='none'">
      <span class="personal-card-body">
        <span class="personal-card-title">${esc(item.name)}</span>
        <span class="personal-card-meta">${item.read_count} of ${item.book_count} volumes read · ${pct}%</span>
        <span class="personal-card-target">${action}: ${esc(item.target_book_title)}</span>
        <span class="personal-progress"><i style="width:${pct}%"></i></span>
      </span>`;
    card.addEventListener('click', () => {
      location.href = `/reader?id=${encodeURIComponent(item.target_book_id)}`;
    });
    continueGrid.appendChild(card);
  }

  for (const item of completed) {
    const card = document.createElement('button');
    card.className = 'personal-card completed';
    card.innerHTML = `
      <img src="${cacheBust(item.cover_url)}" alt="${esc(item.name)}"
           onerror="this.style.display='none'">
      <span class="personal-card-body">
        <span class="personal-card-title">${esc(item.name)}</span>
        <span class="personal-card-meta">${item.book_count} volume${item.book_count === 1 ? '' : 's'}</span>
        <span class="personal-card-target">✓ Fully read</span>
      </span>`;
    card.addEventListener('click', () => showBooks(item.name));
    completedGrid.appendChild(card);
  }
}

function renderSeriesGrid(items, emptyText) {
  grid.innerHTML = '';
  emptyMsg.hidden = true;

  if (items.length === 0) {
    emptyMsg.hidden = false;
    emptyMsg.textContent = emptyText;
    return;
  }

  for (const s of items) {
    const card = document.createElement('div');
    card.className = 'book-card series-card';
    const coverURL = s.cover_url ? cacheBust(s.cover_url) : '';
    card.innerHTML = `
      <div class="series-link" data-series="${esc(s.name)}" style="cursor:pointer">
        ${coverURL
          ? `<img src="${coverURL}" alt="${esc(s.name)}" loading="lazy" onerror="this.style.display='none'">`
          : '<div class="cover-placeholder"></div>'}
        <div class="book-info">
          <div class="book-title">${esc(s.name)}</div>
          <div class="book-author">${s.book_count} volume${s.book_count !== 1 ? 's' : ''}</div>
        </div>
      </div>
      <button class="book-tag-btn series-menu-btn" title="Categories">⋯</button>`;
    card.querySelector('.series-link').addEventListener('click', () => {
      activeTag = '';
      renderTagChips();
      showBooks(s.name);
    });
    card.querySelector('.series-menu-btn').addEventListener('click', e => {
      e.stopPropagation();
      openCategoryEditor(s.name, e.currentTarget);
    });
    grid.appendChild(card);
  }
}

function showTitles(historyMode) {
  showStandardContent();
  currentView = 'titles';
  currentLibrary = null;
  breadcrumb.hidden = true;
  viewTitle.textContent = 'Library';
  setActiveNav(navAllTitles);
  searchInput.placeholder = 'Search titles…';
  syncAppHistory(historyMode || 'push');

  const filtered = searchQuery
    ? allSeries.filter(s => s.name.toLowerCase().includes(searchQuery.toLowerCase()))
    : allSeries;

  renderSeriesGrid(filtered, 'No titles found.');
}

function filterLibrarySeries() {
  const filtered = searchQuery
    ? currentLibrarySeries.filter(s => s.name.toLowerCase().includes(searchQuery.toLowerCase()))
    : currentLibrarySeries;
  renderSeriesGrid(filtered, 'Empty.');
}

async function showLibrary(lib, headerEl, historyMode) {
  showStandardContent();
  currentView = 'library';
  currentLibrary = lib;
  breadcrumb.hidden = false;
  viewTitle.textContent = lib.name;
  searchInput.placeholder = 'Search…';
  activeTag = '';
  renderTagChips();
  if (headerEl) setActiveNav(headerEl);
  syncAppHistory(historyMode || 'push');

  grid.innerHTML = '<p style="padding:1.5rem;color:var(--text-dim)">Loading…</p>';
  emptyMsg.hidden = true;

  let data;
  try {
    data = await fetch(`/api/series?library=${lib.id}`).then(r => r.json());
  } catch {
    grid.innerHTML = '';
    emptyMsg.textContent = 'Failed to load library.';
    emptyMsg.hidden = false;
    return;
  }

  currentLibrarySeries = data.items || [];
  filterLibrarySeries();
}

async function showBooks(seriesName, historyMode) {
  showStandardContent();
  currentView = 'series';
  breadcrumb.hidden = false;
  viewTitle.textContent = seriesName;
  searchInput.placeholder = 'Search…';
  syncAppHistory(historyMode || 'push');

  document.querySelectorAll('.nav-item.active, .library-group-header.active')
    .forEach(el => el.classList.remove('active'));

  grid.innerHTML = '<p style="padding:1.5rem;color:var(--text-dim)">Loading…</p>';
  emptyMsg.hidden = true;

  const url = new URL('/api/books', location.origin);
  url.searchParams.set('limit', '200');
  url.searchParams.set('series', seriesName);
  // Default sort is alphabetical by title, which breaks for Roman-numeral
  // volume suffixes (狼と香辛料IX sorts before 狼と香辛料V as plain text,
  // showing volume 9 in position 5) — series_index is the actual reading
  // order regardless of title text.
  url.searchParams.set('sort', 'series');
  if (searchQuery) url.searchParams.set('q', searchQuery);

  let data;
  try {
    data = await fetch(url).then(r => r.json());
  } catch {
    grid.innerHTML = '';
    emptyMsg.textContent = 'Failed to load books.';
    emptyMsg.hidden = false;
    return;
  }

  renderBookGrid(data.items || []);
}

async function showTaggedBooks(tagName, historyMode) {
  showStandardContent();
  currentView = 'tag';
  breadcrumb.hidden = false;
  viewTitle.textContent = tagName;
  searchInput.placeholder = 'Search…';
  syncAppHistory(historyMode || 'push');
  document.querySelectorAll('.nav-item.active, .library-group-header.active')
    .forEach(el => el.classList.remove('active'));

  grid.innerHTML = '<p style="padding:1.5rem;color:var(--text-dim)">Loading…</p>';
  emptyMsg.hidden = true;

  const url = new URL('/api/books', location.origin);
  url.searchParams.set('limit', '200');
  url.searchParams.set('tag', tagName);
  if (searchQuery) url.searchParams.set('q', searchQuery);

  let data;
  try {
    data = await fetch(url).then(r => r.json());
  } catch {
    grid.innerHTML = '';
    emptyMsg.textContent = 'Failed to load books.';
    emptyMsg.hidden = false;
    return;
  }

  renderBookGrid(data.items || []);
}

function renderBookGrid(books) {
  grid.innerHTML = '';
  if (books.length === 0) {
    emptyMsg.hidden = false;
    emptyMsg.textContent = 'No books found.';
    return;
  }
  for (const b of books) {
    const card = document.createElement('div');
    const pct = b.progress_pct || 0;
    const read = pct >= 1;
    card.className = 'book-card' + (pct > 0 ? ' in-progress' : '') + (read ? ' read' : '');
    const progressHTML = pct > 0 ? `
      <div class="book-progress-bar-wrap">
        <div class="book-progress-bar">
          <div class="book-progress-fill" style="width:${Math.round(pct*100)}%"></div>
        </div>
        <div class="book-progress-pct">${read ? 'Read' : Math.round(pct*100) + '%'}</div>
      </div>` : '';
    card.innerHTML = `
      <a class="book-card-link" href="/reader?id=${b.id}">
        <img src="${cacheBust(`/api/books/${b.id}/cover`)}" alt="${esc(b.title)}" loading="lazy"
             onerror="this.style.display='none'">
        <div class="book-info">
          <div class="book-title">${esc(b.title || '(No title)')}</div>
          ${(b.authors||[]).length ? `<div class="book-author">${esc(b.authors.join(', '))}</div>` : ''}
        </div>
        ${progressHTML}
      </a>
      ${currentUser
        ? `<button class="book-tag-btn" data-id="${b.id}" title="More">⋯</button>`
        : ''}`;
    const menuBtn = card.querySelector('.book-tag-btn');
    if (menuBtn) {
      menuBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        // Re-render from the same array so the card picks up the new read state.
        openBookMenu(b, e.currentTarget, () => renderBookGrid(books));
      });
    }
    grid.appendChild(card);
  }
}

// ── Tag chips ─────────────────────────────────────────────────────────────────

async function loadTags() {
  let data;
  try {
    data = await fetch('/api/tags').then(r => r.json());
  } catch {
    return;
  }
  window._allTags = data.items || [];
  renderTagChips();
}

function renderTagChips() {
  tagChips.innerHTML = '';
  tagChips.appendChild(tagChipsEmpty);
  const tags = window._allTags || [];
  tagChipsEmpty.hidden = tags.length > 0;
  tagChipsEmpty.textContent = 'No genres tagged yet';
  for (const tag of tags) {
    const btn = document.createElement('button');
    btn.className = 'tag-chip' + (activeTag === tag ? ' active' : '');
    btn.textContent = tag;
    btn.addEventListener('click', () => {
      if (activeTag === tag) {
        activeTag = '';
        renderTagChips();
        showTitles();
      } else {
        activeTag = tag;
        renderTagChips();
        showTaggedBooks(tag);
      }
    });
    tagChips.appendChild(btn);
  }
}

// ── Book card "⋯" menu ────────────────────────────────────────────────────────

let bookMenuPopup = null;

function closeBookMenu() {
  if (bookMenuPopup) { bookMenuPopup.remove(); bookMenuPopup = null; }
}

document.addEventListener('click', (e) => {
  if (bookMenuPopup && !bookMenuPopup.contains(e.target)) closeBookMenu();
});

function placePopup(popup, anchorEl) {
  const rect = anchorEl.getBoundingClientRect();
  let left = rect.right - popup.offsetWidth;
  if (left < 4) left = 4;
  popup.style.left = left + 'px';
  popup.style.top  = (rect.bottom + 6) + 'px';
}

// Read state is per-user, so every logged-in user gets this menu; genre editing
// stays admin-only. onChanged redraws the grid once the server has confirmed.
function openBookMenu(book, anchorEl, onChanged) {
  closeBookMenu();
  closeTagEditor();

  const isRead = (book.progress_pct || 0) >= 1;
  const popup = document.createElement('div');
  popup.className = 'book-menu-popup';
  popup.addEventListener('click', e => e.stopPropagation());
  bookMenuPopup = popup;

  popup.innerHTML = `
    <button class="book-menu-item" data-act="read">${isRead ? 'Mark as unread' : 'Mark as read'}</button>
    ${book.series_name
      ? '<button class="book-menu-item" data-act="categories">Categories</button>'
      : ''}
    ${currentUser && currentUser.is_admin
      ? '<button class="book-menu-item" data-act="genres">Edit genres</button>'
      : ''}`;

  popup.querySelector('[data-act="read"]').addEventListener('click', async () => {
    const read = !isRead;
    closeBookMenu();
    const res = await fetch(`/api/books/${book.id}/read`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ read }),
    });
    if (!res.ok) return;
    book.progress_pct = read ? 1 : 0;
    onChanged();
  });

  const categoriesBtn = popup.querySelector('[data-act="categories"]');
  if (categoriesBtn) {
    categoriesBtn.addEventListener('click', () => {
      closeBookMenu();
      openCategoryEditor(book.series_name, anchorEl);
    });
  }

  const genresBtn = popup.querySelector('[data-act="genres"]');
  if (genresBtn) {
    genresBtn.addEventListener('click', () => {
      closeBookMenu();
      openTagEditor(book.id, book.title, anchorEl);
    });
  }

  document.body.appendChild(popup);
  placePopup(popup, anchorEl);
}

// ── Personal categories ──────────────────────────────────────────────────────

let categoryEditorPopup = null;

async function loadPersonalCategories() {
  const res = await fetch('/api/me/categories');
  if (!res.ok) throw new Error('could not load categories');
  return (await res.json()).items || [];
}

async function refreshPersonalCategories() {
  personalCategories = await loadPersonalCategories();
  if (currentView === 'personal') renderPersonalCategories();
}

function closeCategoryEditor() {
  if (categoryEditorPopup) {
    categoryEditorPopup.remove();
    categoryEditorPopup = null;
  }
}

document.addEventListener('click', e => {
  if (categoryEditorPopup && !categoryEditorPopup.contains(e.target)) closeCategoryEditor();
});

async function createCategory(name) {
  const res = await fetch('/api/me/categories', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) return null;
  const category = await res.json();
  personalCategories.push(category);
  return category;
}

async function setCategorySeries(categoryId, seriesName, included) {
  const res = await fetch(`/api/me/categories/${categoryId}/series`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ series_name: seriesName, included }),
  });
  if (res.ok) await refreshPersonalCategories();
  return res.ok;
}

async function openCategoryEditor(seriesName, anchorEl) {
  closeCategoryEditor();
  closeBookMenu();
  try {
    personalCategories = await loadPersonalCategories();
  } catch {
    return;
  }

  const popup = document.createElement('div');
  popup.className = 'tag-editor-popup category-editor-popup';
  popup.addEventListener('click', e => e.stopPropagation());
  categoryEditorPopup = popup;

  function render() {
    popup.innerHTML = `
      <div class="tag-editor-title">Categories · ${esc(seriesName)}</div>
      <div class="category-editor-list">
        ${personalCategories.map(category => {
          const checked = (category.items || []).some(item => item.name === seriesName);
          return `<label class="category-check">
            <input type="checkbox" data-category="${category.id}" ${checked ? 'checked' : ''}>
            <span>${esc(category.name)}</span>
          </label>`;
        }).join('')}
      </div>
      <div class="tag-editor-add">
        <input class="tag-editor-input" maxlength="60" placeholder="New category…">
        <button class="tag-editor-save">Create</button>
      </div>`;

    popup.querySelectorAll('[data-category]').forEach(input => {
      input.addEventListener('change', async () => {
        input.disabled = true;
        const ok = await setCategorySeries(input.dataset.category, seriesName, input.checked);
        if (!ok) input.checked = !input.checked;
        input.disabled = false;
      });
    });

    const input = popup.querySelector('.tag-editor-input');
    const submit = async () => {
      const name = input.value.trim();
      if (!name) return;
      const category = await createCategory(name);
      if (!category) return;
      await setCategorySeries(category.id, seriesName, true);
      personalCategories = await loadPersonalCategories();
      render();
    };
    popup.querySelector('.tag-editor-save').addEventListener('click', submit);
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') submit();
    });
  }

  render();
  document.body.appendChild(popup);
  placePopup(popup, anchorEl);
}

function categorySeriesCard(item) {
  const card = document.createElement('button');
  card.className = 'personal-card category-series-card';
  card.innerHTML = `
    <img src="${cacheBust(item.cover_url)}" alt="${esc(item.name)}"
         onerror="this.style.display='none'">
    <span class="personal-card-body">
      <span class="personal-card-title">${esc(item.name)}</span>
      <span class="personal-card-meta">${item.book_count} volume${item.book_count === 1 ? '' : 's'}</span>
      <span class="personal-card-target">Open title</span>
    </span>`;
  card.addEventListener('click', () => showBooks(item.name));
  return card;
}

function renderPersonalCategories() {
  const root = document.getElementById('personal-categories');
  root.replaceChildren();
  for (const category of personalCategories) {
    const section = document.createElement('section');
    section.className = 'personal-category';
    const header = document.createElement('div');
    header.className = 'personal-category-header';
    const title = document.createElement('h3');
    title.textContent = category.name;
    const count = document.createElement('span');
    count.textContent = `${(category.items || []).length} title${(category.items || []).length === 1 ? '' : 's'}`;
    header.append(title, count);
    if (!category.is_system) {
      const actions = document.createElement('span');
      actions.className = 'personal-category-actions';
      const rename = document.createElement('button');
      rename.textContent = 'Rename';
      const remove = document.createElement('button');
      remove.textContent = 'Delete';
      remove.className = 'danger';
      rename.addEventListener('click', async () => {
        const name = prompt('Category name', category.name);
        if (!name || name.trim() === category.name) return;
        const res = await fetch(`/api/me/categories/${category.id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: name.trim() }),
        });
        if (res.ok) await refreshPersonalCategories();
      });
      remove.addEventListener('click', async () => {
        if (!confirm(`Delete category “${category.name}”?`)) return;
        const res = await fetch(`/api/me/categories/${category.id}`, { method: 'DELETE' });
        if (res.ok) await refreshPersonalCategories();
      });
      actions.append(rename, remove);
      header.appendChild(actions);
    }
    section.appendChild(header);
    const categoryGrid = document.createElement('div');
    categoryGrid.className = 'personal-grid';
    for (const item of category.items || []) categoryGrid.appendChild(categorySeriesCard(item));
    if (!(category.items || []).length) {
      const empty = document.createElement('p');
      empty.className = 'category-empty';
      empty.textContent = 'Use the ⋯ menu on a title to add it here.';
      section.appendChild(empty);
    } else {
      section.appendChild(categoryGrid);
    }
    root.appendChild(section);
  }
}

document.getElementById('category-create').addEventListener('click', async () => {
  const name = prompt('New category name');
  if (!name) return;
  if (await createCategory(name.trim())) await refreshPersonalCategories();
});

// ── Tag editor popup ──────────────────────────────────────────────────────────

let tagEditorPopup = null;

function closeTagEditor() {
  if (tagEditorPopup) { tagEditorPopup.remove(); tagEditorPopup = null; }
}

document.addEventListener('click', (e) => {
  if (tagEditorPopup && !tagEditorPopup.contains(e.target)) closeTagEditor();
});

async function openTagEditor(bookId, bookTitle, anchorEl) {
  closeTagEditor();

  const data = await fetch(`/api/books/${bookId}/tags`).then(r => r.json()).catch(() => ({ items: [] }));
  let tags = data.items || [];

  const popup = document.createElement('div');
  popup.className = 'tag-editor-popup';
  popup.addEventListener('click', e => e.stopPropagation());
  tagEditorPopup = popup;

  function render() {
    popup.innerHTML = `
      <div class="tag-editor-title">${esc(bookTitle)}</div>
      <div class="tag-editor-list">${tags.length
        ? tags.map(t => `<span class="tag-editor-chip">${esc(t)}<button class="tag-rm" data-tag="${esc(t)}">×</button></span>`).join('')
        : '<span style="color:var(--text-dim);font-size:.8rem">No genres yet</span>'
      }</div>
      <div class="tag-editor-add">
        <input class="tag-editor-input" placeholder="Add genre…" list="tag-suggestions">
        <datalist id="tag-suggestions">${(window._allTags||[]).map(t => `<option value="${esc(t)}">`).join('')}</datalist>
        <button class="tag-editor-save">Add</button>
      </div>`;

    popup.querySelectorAll('.tag-rm').forEach(btn => {
      btn.addEventListener('click', async () => {
        tags = tags.filter(t => t !== btn.dataset.tag);
        await saveTags();
      });
    });

    popup.querySelector('.tag-editor-save').addEventListener('click', async () => {
      const input = popup.querySelector('.tag-editor-input');
      const val = input.value.trim();
      if (!val || tags.includes(val)) { input.value = ''; return; }
      tags = [...tags, val];
      input.value = '';
      await saveTags();
    });

    popup.querySelector('.tag-editor-input').addEventListener('keydown', async (e) => {
      if (e.key !== 'Enter') return;
      const val = e.target.value.trim();
      if (!val || tags.includes(val)) { e.target.value = ''; return; }
      tags = [...tags, val];
      e.target.value = '';
      await saveTags();
    });
  }

  async function saveTags() {
    await fetch(`/api/books/${bookId}/tags`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tags }),
    });
    render();
    loadTags(); // refresh tag chips
  }

  render();

  // Position popup near the anchor button
  document.body.appendChild(popup);
  const rect = anchorEl.getBoundingClientRect();
  const pw = popup.offsetWidth;
  let left = rect.right - pw;
  if (left < 4) left = 4;
  popup.style.left = left + 'px';
  popup.style.top  = (rect.bottom + 6) + 'px';
}

// ── Series (all titles grid) ─────────────────────────────────────────────────

async function loadSeriesData() {
  try {
    const data = await fetch('/api/series?exclude_html=1').then(r => r.json());
    allSeries = data.items || [];
  } catch {
    allSeries = [];
  }
}

// ── Library tabs (top bar) ────────────────────────────────────────────────────

let loadedLibraries = [];

async function loadLibraries() {
  let libs;
  try {
    libs = (await fetch('/api/libraries').then(r => r.json())).items || [];
  } catch {
    libraryGroups.innerHTML = '<span class="nav-loading">Failed.</span>';
    return;
  }

  loadedLibraries = libs;
  libraryGroups.innerHTML = '';
  if (libs.length === 0) {
    libraryGroups.innerHTML = '<span class="nav-loading">No libraries yet.</span>';
    return;
  }

  for (const lib of libs) {
    const header = document.createElement('button');
    header.className = 'library-group-header';
    header.innerHTML = `<span class="library-group-name">${esc(lib.name)}</span>`;
    header.addEventListener('click', () => showLibrary(lib, header));
    libraryGroups.appendChild(header);
  }
}

// ── Breadcrumb ────────────────────────────────────────────────────────────────

document.getElementById('btn-back').addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showPersonalLibrary();
});

navMyLibrary.addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showPersonalLibrary();
});

navAllTitles.addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showTitles();
});

// ── Logo ──────────────────────────────────────────────────────────────────────

document.getElementById('logo-home').addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showPersonalLibrary();
});

// ── Search ────────────────────────────────────────────────────────────────────

function doSearch() {
  if (activeTag) {
    showTaggedBooks(activeTag, 'replace');
  } else if (currentView === 'library') {
    filterLibrarySeries();
    syncAppHistory('replace');
  } else if (currentView === 'series') {
    showBooks(viewTitle.textContent, 'replace');
  } else {
    showTitles('replace');
  }
}

btnSearch.addEventListener('click', doSearch);

searchInput.addEventListener('input', e => {
  searchQuery = e.target.value.trim();
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(doSearch, 280);
});

searchInput.addEventListener('keydown', e => {
  if (e.key === 'Enter') { clearTimeout(debounceTimer); doSearch(); }
});

// ── Nav helpers ───────────────────────────────────────────────────────────────

function setActiveNav(el) {
  document.querySelectorAll('.nav-item.active, .library-group-header.active')
    .forEach(n => n.classList.remove('active'));
  el.classList.add('active');
}

// ── Scroll-hide header ────────────────────────────────────────────────────────

const mainHeader = document.querySelector('.main-header');
let lastY = 0;
window.addEventListener('scroll', () => {
  const y = window.scrollY;
  mainHeader.classList.toggle('header-hidden', y > lastY && y > 56);
  lastY = y;
}, { passive: true });

// ── Helpers ───────────────────────────────────────────────────────────────────

function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// ── Init ──────────────────────────────────────────────────────────────────────

// Restores whichever view the current URL describes (a deep link, a reload, or
// a Back/Forward step) instead of always landing on the home "All Titles"
// grid. Needs loadedLibraries/allSeries already populated.
async function restoreFromURL() {
  const params = new URLSearchParams(location.search);
  searchQuery = params.get('q') || '';
  searchInput.value = searchQuery;
  const view = params.get('view');

  if (view === 'titles') {
    showTitles('none');
    return;
  } else if (view === 'tag') {
    const tag = params.get('tag');
    if (tag) {
      activeTag = tag;
      renderTagChips();
      await showTaggedBooks(tag, 'none');
      return;
    }
  } else if (view === 'series') {
    const series = params.get('series');
    if (series) {
      await showBooks(series, 'none');
      return;
    }
  } else if (view === 'library') {
    const lib = loadedLibraries.find(l => l.id === params.get('lib'));
    if (lib) {
      const header = [...libraryGroups.querySelectorAll('.library-group-header')]
        .find(h => h.textContent === lib.name);
      await showLibrary(lib, header, 'none');
      return;
    }
  }
  await showPersonalLibrary('none');
}

checkAuth().then(async ok => {
  if (!ok) return;
  if (!currentUser.is_admin) {
    const settingsLink = document.getElementById('settings-link');
    if (settingsLink) settingsLink.hidden = true;
  }
  loadTags();
  await Promise.all([loadLibraries(), loadSeriesData()]);
  await restoreFromURL();
});
