'use strict';
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const state = { me: null, view: 'devices', devices: [], servers: [], deleting: null };
const platforms = { ios: 'iOS / iPadOS', android: 'Android', windows: 'Windows', macos: 'macOS', gateway: 'Домашний шлюз' };
const actions = { 'device.created': 'Устройство добавлено', 'device.deleted': 'Устройство удалено', 'server.registered': 'Сервер зарегистрирован' };
let toastTimer;
function node(tag, className, text) { const el = document.createElement(tag); if (className) el.className = className; if (text !== undefined) el.textContent = text; return el; }
function notify(text) { $('#toast').textContent = text; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 4000); }
async function api(path, options = {}) {
 const headers = { ...options.headers };
 if (options.body) headers['Content-Type'] = 'application/json';
 if (state.me) headers['X-CSRF-Token'] = state.me.csrf_token;
 const response = await fetch(path, { ...options, headers, credentials: 'same-origin' });
 if (response.status === 204) return null;
 const data = await response.json();
 if (!response.ok) {
  if (response.status === 401 && state.me) { state.me = null; showLogin(); $$('dialog[open]').forEach(d => d.close()); }
  throw new Error(data.error || 'Не удалось выполнить запрос');
 }
 return data;
}
function showLogin() { $('#shell').hidden = true; $('#login').hidden = false; }
async function start() {
 try {
  const publicConfig = await api('/api/public');
  $('#dev-login').hidden = !publicConfig.dev_auth; $('#sso-login').hidden = publicConfig.dev_auth;
  if (publicConfig.dev_auth) $('#login-note').textContent = 'Локальный режим разработки. Только на этом компьютере.';
  try { state.me = await api('/api/me'); } catch (error) { showLogin(); if (!error.message.includes('Войдите') && !error.message.includes('Сессия')) $('#login-error').textContent = error.message; return; }
  $('#login').hidden = true; $('#shell').hidden = false;
  const admin = state.me.user.role === 'admin';
  $$('[data-admin]').forEach(el => { el.hidden = !admin; if (el.tagName === 'OPTION') el.disabled = !admin; });
  $('#username').textContent = state.me.user.username; $('#role').textContent = admin ? 'Администратор' : 'Пользователь';
  $('#avatar').textContent = state.me.user.username.slice(0, 1).toUpperCase(); $('#dev-badge').hidden = !state.me.dev_auth;
  $('#device-list-title').textContent = admin ? 'Все устройства' : 'Ваши устройства';
  $('.stat small').textContent = admin ? 'У всех пользователей' : 'В вашем кабинете';
  await changeView('devices');
 } catch (error) { showLogin(); $('#login-error').textContent = error.message; }
 finally { $('#loading').hidden = true; }
}
async function loadDevices() {
 state.devices = await api('/api/devices');
 const container = $('#devices'); container.replaceChildren();
 $('#device-empty').hidden = state.devices.length > 0;
 $('#device-count').textContent = state.devices.length; $('#nav-count').textContent = state.devices.length;
 $('#platform-count').textContent = new Set(state.devices.map(d => d.platform)).size;
 $('#device-summary').textContent = `${state.devices.length} всего`;
 for (const device of state.devices) {
  const card = node('article', 'device-card'); const top = node('div', 'card-top');
  const icon = node('span', 'platform-icon', ['ios', 'android'].includes(device.platform) ? '▯' : '▱'); icon.setAttribute('aria-hidden', 'true');
  const remove = node('button', 'icon-button', '×'); remove.setAttribute('aria-label', `Удалить ${device.name}`);
  remove.addEventListener('click', () => { state.deleting = device.id; $('#delete-description').textContent = `Запись «${device.name}» будет удалена из кабинета. VPN-профиль для неё ещё не выпускался.`; openDialog('delete'); });
  top.append(icon, remove); card.append(top, node('h3', '', device.name), node('p', 'platform', platforms[device.platform]));
  const meta = node('div', 'device-meta'); meta.append(node('span', '', device.owner), node('span', 'badge', 'Профиль не выпущен')); card.append(meta); container.append(card);
 }
}
async function loadServers() {
 state.servers = await api('/api/admin/servers'); const container = $('#servers'); container.replaceChildren(); $('#server-empty').hidden = state.servers.length > 0;
 for (const server of state.servers) {
  const card = node('article', 'server-card'); const top = node('div', 'card-top'); top.append(node('h3', '', server.name), node('span', 'badge', 'Не развёрнут'));
  card.append(top, node('p', 'server-address', `${server.ssh_user}@${server.hostname}:${server.ssh_port}`), node('p', 'server-address', server.host_key_fingerprint)); container.append(card);
 }
}
async function loadEvents() {
 const events = await api('/api/admin/events'); const tbody = $('#events'); tbody.replaceChildren(); $('#event-empty').hidden = events.length > 0; $('.table-wrap').hidden = !events.length;
 for (const event of events) { const row = node('tr'); row.append(node('td', '', actions[event.action] || event.action), node('td', '', event.actor), node('td', '', new Date(event.created_at).toLocaleString('ru-RU'))); tbody.append(row); }
}
async function changeView(view) {
 if (!state.me || (view !== 'devices' && state.me.user.role !== 'admin')) return;
 state.view = view;
 const title = { devices: 'Устройства', servers: 'Серверы', events: 'Журнал изменений' }[view];
 $('#page-title').textContent = title; $('#breadcrumb').textContent = `Личный кабинет / ${title}`;
 $('#page-eyebrow').textContent = view === 'devices' ? 'ЛИЧНЫЙ КАБИНЕТ' : 'УПРАВЛЕНИЕ';
 $('#page-description').textContent = { devices: 'Отдельная запись для каждого подключения.', servers: 'Ваши VPS и параметры доступа.', events: 'Последние 100 изменений в Củ Chi.' }[view];
 $('#add').hidden = view === 'events'; $('#add').textContent = view === 'devices' ? '＋ Добавить устройство' : '＋ Добавить сервер';
 for (const name of ['devices', 'servers', 'events']) $(`#${name}-view`).hidden = name !== view;
 $$('[data-view]').forEach(el => el.classList.toggle('active', el.dataset.view === view));
 $('#page-error').hidden = true;
 try { await ({ devices: loadDevices, servers: loadServers, events: loadEvents }[view])(); }
 catch (error) { $('#page-error').textContent = error.message; $('#page-error').hidden = false; }
}
function openDialog(name) { const dialog = $(`#${name}-dialog`); $('.form-error', dialog).textContent = ''; dialog.showModal(); }
function bindForm(name, handler) {
 $(`#${name}-form`).addEventListener('submit', async event => {
  event.preventDefault(); const form = event.currentTarget; const submit = $('button[type="submit"]', form); submit.disabled = true; $('.form-error', form).textContent = '';
  try { await handler(form); } catch (error) { $('.form-error', form).textContent = error.message; } finally { submit.disabled = false; }
 });
}
$$('[data-close]').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
$$('dialog').forEach(dialog => dialog.addEventListener('close', () => { if (dialog.id === 'server-dialog') $('textarea', dialog).value = ''; }));
$$('[data-view]').forEach(button => button.addEventListener('click', () => changeView(button.dataset.view)));
$('#add').addEventListener('click', () => openDialog(state.view === 'devices' ? 'device' : 'server'));
$('#empty-add-device').addEventListener('click', () => openDialog('device')); $('#empty-add-server').addEventListener('click', () => openDialog('server'));
$('#logout').addEventListener('click', async () => { try { await api('/api/logout', { method: 'POST' }); state.me = null; location.reload(); } catch (error) { notify(error.message); } });
$('#dev-login').addEventListener('click', async event => { event.currentTarget.disabled = true; try { await api('/auth/dev', { method: 'POST' }); await start(); } catch (error) { $('#login-error').textContent = error.message; } finally { $('#dev-login').disabled = false; } });
bindForm('device', async form => { await api('/api/devices', { method: 'POST', body: JSON.stringify(Object.fromEntries(new FormData(form))) }); $('#device-dialog').close(); form.reset(); await loadDevices(); notify('Устройство добавлено'); });
bindForm('server', async form => { const data = Object.fromEntries(new FormData(form)); data.ssh_port = Number(data.ssh_port); await api('/api/admin/servers', { method: 'POST', body: JSON.stringify(data) }); $('#server-dialog').close(); form.reset(); await loadServers(); notify('Сервер сохранён'); });
bindForm('delete', async () => { await api(`/api/devices/${state.deleting}`, { method: 'DELETE' }); $('#delete-dialog').close(); state.deleting = null; await loadDevices(); notify('Устройство удалено'); });
start();
