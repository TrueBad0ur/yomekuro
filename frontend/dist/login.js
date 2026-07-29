'use strict';

// If already authenticated, skip to library
fetch('/api/auth/me').then(r => {
  if (r.ok) location.href = '/';
}).catch(() => {});

const tabLogin    = document.getElementById('tab-login');
const tabRegister = document.getElementById('tab-register');
const loginForm   = document.getElementById('login-form');
const regForm     = document.getElementById('register-form');
const loginErr    = document.getElementById('login-error');
const regErr      = document.getElementById('reg-error');
const googleBtn   = document.getElementById('google-login-btn');
const loginDivider = document.getElementById('login-divider');

fetch('/api/auth/providers').then(r => r.json()).then(providers => {
  if (providers.google) {
    googleBtn.hidden = false;
    loginDivider.hidden = false;
  }
}).catch(() => {});

const oauthError = new URLSearchParams(location.search).get('error');
if (oauthError) {
  loginErr.textContent = oauthError;
  loginErr.hidden = false;
  history.replaceState({}, '', '/login');
}

tabLogin.addEventListener('click', () => {
  tabLogin.classList.add('active');
  tabRegister.classList.remove('active');
  loginForm.hidden = false;
  regForm.hidden = true;
});

tabRegister.addEventListener('click', () => {
  tabRegister.classList.add('active');
  tabLogin.classList.remove('active');
  regForm.hidden = false;
  loginForm.hidden = true;
});

loginForm.addEventListener('submit', async e => {
  e.preventDefault();
  loginErr.hidden = true;
  const username = document.getElementById('login-username').value.trim();
  const password = document.getElementById('login-password').value;
  try {
    const res = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      loginErr.textContent = d.error || 'Invalid credentials';
      loginErr.hidden = false;
      return;
    }
    location.href = '/';
  } catch {
    loginErr.textContent = 'Network error';
    loginErr.hidden = false;
  }
});

regForm.addEventListener('submit', async e => {
  e.preventDefault();
  regErr.hidden = true;
  const username = document.getElementById('reg-username').value.trim();
  const password = document.getElementById('reg-password').value;
  try {
    const res = await fetch('/api/auth/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      regErr.textContent = d.error || 'Registration failed';
      regErr.hidden = false;
      return;
    }
    location.href = '/';
  } catch {
    regErr.textContent = 'Network error';
    regErr.hidden = false;
  }
});
