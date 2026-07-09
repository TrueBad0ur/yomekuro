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

// ── Sidebar ───────────────────────────────────────────────────────────────────

const sidebar      = document.getElementById('sidebar');
const overlay      = document.getElementById('sidebar-overlay');
const hamburger    = document.getElementById('hamburger');
const sidebarClose = document.getElementById('sidebar-close');

function openSidebar()  { sidebar.classList.add('open');    overlay.classList.add('visible'); }
function closeSidebar() { sidebar.classList.remove('open'); overlay.classList.remove('visible'); }

hamburger.addEventListener('click', openSidebar);
sidebarClose.addEventListener('click', closeSidebar);
overlay.addEventListener('click', closeSidebar);

// ── State ─────────────────────────────────────────────────────────────────────

let allSeries   = [];
let searchQuery = '';
let activeTag   = '';
let debounceTimer = null;
let currentView = 'titles'; // 'titles' | 'series' | 'tag' | 'library'
let currentLibrary = null;
let currentLibrarySeries = [];

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
const navAllTitles  = document.getElementById('nav-all-titles');

// ── Views ─────────────────────────────────────────────────────────────────────

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
    const coverURL = s.cover_url || '';
    card.innerHTML = `
      <div class="series-link" data-series="${esc(s.name)}" style="cursor:pointer">
        ${coverURL
          ? `<img src="${coverURL}" alt="${esc(s.name)}" loading="lazy" onerror="this.style.display='none'">`
          : '<div class="cover-placeholder"></div>'}
        <div class="book-info">
          <div class="book-title">${esc(s.name)}</div>
          <div class="book-author">${s.book_count} volume${s.book_count !== 1 ? 's' : ''}</div>
        </div>
      </div>`;
    card.querySelector('.series-link').addEventListener('click', () => {
      activeTag = '';
      renderTagChips();
      showBooks(s.name);
      closeSidebar();
    });
    grid.appendChild(card);
  }
}

function showTitles() {
  currentView = 'titles';
  currentLibrary = null;
  breadcrumb.hidden = true;
  viewTitle.textContent = 'Library';
  setActiveNav(navAllTitles);
  searchInput.placeholder = 'Search titles…';

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

async function showLibrary(lib, headerEl) {
  currentView = 'library';
  currentLibrary = lib;
  breadcrumb.hidden = false;
  viewTitle.textContent = lib.name;
  searchInput.placeholder = 'Search…';
  activeTag = '';
  renderTagChips();
  if (headerEl) setActiveNav(headerEl);
  closeSidebar();

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

async function showBooks(seriesName) {
  currentView = 'series';
  breadcrumb.hidden = false;
  viewTitle.textContent = seriesName;
  searchInput.placeholder = 'Search…';

  document.querySelectorAll('.nav-item.active, .library-group-header.active')
    .forEach(el => el.classList.remove('active'));

  grid.innerHTML = '<p style="padding:1.5rem;color:var(--text-dim)">Loading…</p>';
  emptyMsg.hidden = true;

  const url = new URL('/api/books', location.origin);
  url.searchParams.set('limit', '200');
  url.searchParams.set('series', seriesName);
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

async function showTaggedBooks(tagName) {
  currentView = 'tag';
  breadcrumb.hidden = false;
  viewTitle.textContent = tagName;
  searchInput.placeholder = 'Search…';
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
    card.className = 'book-card' + (pct > 0 ? ' in-progress' : '');
    const progressHTML = pct > 0 ? `
      <div class="book-progress-bar-wrap">
        <div class="book-progress-bar">
          <div class="book-progress-fill" style="width:${Math.round(pct*100)}%"></div>
        </div>
        <div class="book-progress-pct">${Math.round(pct*100)}%</div>
      </div>` : '';
    card.innerHTML = `
      <a class="book-card-link" href="/reader?id=${b.id}">
        <img src="/api/books/${b.id}/cover" alt="${esc(b.title)}" loading="lazy"
             onerror="this.style.display='none'">
        <div class="book-info">
          <div class="book-title">${esc(b.title || '(No title)')}</div>
          ${(b.authors||[]).length ? `<div class="book-author">${esc(b.authors.join(', '))}</div>` : ''}
        </div>
        ${progressHTML}
      </a>
      ${currentUser && currentUser.is_admin
        ? `<button class="book-tag-btn" data-id="${b.id}" title="Edit genres">⋯</button>`
        : ''}`;
    const tagBtn = card.querySelector('.book-tag-btn');
    if (tagBtn) {
      tagBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        openTagEditor(b.id, b.title, e.currentTarget);
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
        closeSidebar();
      }
    });
    tagChips.appendChild(btn);
  }
}

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
    loadTags(); // refresh tag chips in sidebar
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

async function loadSeries() {
  let data;
  try {
    data = await fetch('/api/series?exclude_html=1').then(r => r.json());
  } catch {
    allSeries = [];
    showTitles();
    return;
  }

  allSeries = data.items || [];
  showTitles();
}

// ── Library tree (sidebar) ───────────────────────────────────────────────────

async function loadLibraries() {
  let libs;
  try {
    libs = (await fetch('/api/libraries').then(r => r.json())).items || [];
  } catch {
    libraryGroups.innerHTML = '<span class="nav-loading">Failed.</span>';
    return;
  }

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
  showTitles();
});

navAllTitles.addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showTitles();
  closeSidebar();
});

// ── Logo ──────────────────────────────────────────────────────────────────────

document.getElementById('logo-home').addEventListener('click', () => {
  searchQuery = '';
  searchInput.value = '';
  activeTag = '';
  renderTagChips();
  showTitles();
  closeSidebar();
});

// ── Search ────────────────────────────────────────────────────────────────────

function doSearch() {
  if (activeTag) {
    showTaggedBooks(activeTag);
  } else if (currentView === 'library') {
    filterLibrarySeries();
  } else if (currentView === 'series') {
    showBooks(viewTitle.textContent);
  } else {
    showTitles();
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
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ── Init ──────────────────────────────────────────────────────────────────────

checkAuth().then(ok => {
  if (!ok) return;
  if (!currentUser.is_admin) {
    const settingsLink = document.querySelector('.sidebar-footer .footer-link');
    if (settingsLink) settingsLink.hidden = true;
  }
  loadSeries();
  loadLibraries();
  loadTags();
});
