'use strict';

const grid = document.getElementById('books-grid');
const emptyMsg = document.getElementById('empty-msg');
const searchInput = document.getElementById('search-input');

let debounceTimer = null;

async function load(q) {
  const url = new URL('/api/books', location.origin);
  url.searchParams.set('limit', '200');
  if (q) url.searchParams.set('q', q);

  let data;
  try {
    const res = await fetch(url);
    data = await res.json();
  } catch {
    grid.innerHTML = '';
    emptyMsg.hidden = false;
    emptyMsg.textContent = 'Failed to load books.';
    return;
  }

  const books = data.items || [];
  grid.innerHTML = '';

  if (books.length === 0) {
    emptyMsg.hidden = false;
    return;
  }
  emptyMsg.hidden = true;

  for (const b of books) {
    const card = document.createElement('div');
    card.className = 'book-card';

    const coverURL = `/api/books/${b.id}/cover`;
    const authors = (b.authors || []).join(', ');
    const series = b.series_name
      ? `<div class="book-series">${esc(b.series_name)}${b.series_index ? ' #' + b.series_index : ''}</div>`
      : '';

    card.innerHTML = `
      <a href="/reader.html?id=${b.id}">
        <img src="${coverURL}" alt="${esc(b.title)}" loading="lazy"
             onerror="this.style.display='none'">
        <div class="book-info">
          <div class="book-title">${esc(b.title || '(No title)')}</div>
          ${authors ? `<div class="book-author">${esc(authors)}</div>` : ''}
          ${series}
        </div>
      </a>`;
    grid.appendChild(card);
  }
}

function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

searchInput.addEventListener('input', () => {
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => load(searchInput.value.trim()), 300);
});

load('');
