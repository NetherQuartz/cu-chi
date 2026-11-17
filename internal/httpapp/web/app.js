'use strict';
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const state = { me: null, view: 'devices', devices: [], servers: [], deleting: null, profiles: [], vpn: {}, jobs: [], sshKeys: [], deploying: null, profileAction: null, loading: false };
const platforms = { ios: 'iOS / iPadOS', android: 'Android', windows: 'Windows', macos: 'macOS', linux: 'Linux', gateway: 'Домашний шлюз' };
const actions = { 'notifications.reminded':'Отправлены напоминания', 'server.maintenance_changed':'Режим обслуживания изменён', 'server.location_changed':'Местоположение изменено', 'ssh_key.created':'SSH-ключ сохранён', 'ssh_key.deleted':'SSH-ключ удалён', 'profile.issued':'Профиль запрошен', 'profile.revocation_requested':'Запрошен отзыв профилей', 'server.check_queued':'Запрошена проверка SSH', 'server.deploy_queued':'Запрошен деплой', 'server.default_changed':'Основной сервер изменён', 'device.created': 'Устройство добавлено', 'device.deleted': 'Устройство удалено', 'server.registered': 'Сервер зарегистрирован' };
let toastTimer, renderedSSHKeys;
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
  await changeView(new URLSearchParams(location.search).get('view')==='notifications'?'notifications':'devices');
  updateNoticeCount();
 } catch (error) { showLogin(); $('#login-error').textContent = error.message; }
 finally { $('#loading').hidden = true; }
}
async function loadDevices() {
 [state.devices,state.profiles,state.vpn,state.services,state.health] = await Promise.all([api('/api/devices'),api('/api/profiles'),api('/api/vpn'),api('/api/services'),api('/api/health')]);
 const container = $('#devices'); container.replaceChildren();
 $('#device-empty').hidden = state.devices.length > 0;
 $('#device-count').textContent = state.devices.length; $('#nav-count').textContent = state.devices.length;
 $('#platform-count').textContent = new Set(state.devices.map(d => d.platform)).size;
 $('#device-summary').textContent = `${state.devices.length} всего`;
 for (const device of state.devices) {
  const card = node('article', 'device-card'); const top = node('div', 'card-top');
  const icon = node('span', 'platform-icon'); icon.setAttribute('aria-hidden', 'true'); icon.append(node('span', `os-logo os-${Object.hasOwn(platforms,device.platform)?device.platform:'gateway'}`));
  const remove = node('button', 'icon-button', '×'); remove.setAttribute('aria-label', `Удалить ${device.name}`);
  remove.addEventListener('click', () => { state.deleting = device.id; $('#delete-description').textContent = `Запись «${device.name}» будет удалена. Сначала отзовите выпущенные профили и дождитесь подтверждения всех серверов.`; openDialog('delete'); });
  top.append(icon, remove); card.append(top, node('h3', '', device.name), node('p', 'platform', platforms[device.platform]));
  const profiles = state.profiles.filter(p => p.device_id === device.id);
  const current = profiles.find(p => p.desired);
  const meta = node('div', 'device-meta'); meta.append(node('span', '', device.owner), node('span', 'badge', current ? (current.state === 'ready' ? 'Профиль готов' : 'Применяется на сервере') : 'Профиль не выпущен')); card.append(meta);
  if (profiles.some(p=>p.needs_attention)) card.append(node('p','form-error','Не удалось применить изменения. Администратору нужно проверить сервер.'));
  if (current) {card.append(node('p', 'small muted', `${current.server_name} · версия ${current.revision}`));const service=state.services.find(s=>s.id===current.server_id);if(service){card.append(locationLabel(service),node('p','small muted',serviceStatus(service)));}if(current.state==='ready')card.append(node('p',current.server_id!==state.vpn.server_id?'form-error':'small muted',current.server_id!==state.vpn.server_id?'Доступен новый основной сервер — перевыпустите профиль':'Профиль для текущего сервера'));}
  if (profiles.some(p => p.state === 'revoking')) card.append(node('p','form-error','Отзыв старых ключей ещё не подтверждён сервером.'));
  const controls = node('div','card-actions');
  if (current?.state === 'ready') {
   const download=node('a','button secondary','Скачать .conf'); download.href=`/api/profiles/${current.id}/download`; controls.append(download);
   const qr=node('button','button secondary','QR'); qr.addEventListener('click',()=>{ $('#profile-qr').src=`/api/profiles/${current.id}/qr`; $('#qr-dialog').showModal(); }); controls.append(qr);
  }
  const issue=node('button','button secondary',current?'Перевыпустить':'Выпустить профиль'); issue.disabled=!state.vpn.available || current?.state==='pending';
  issue.addEventListener('click',()=>confirmProfile(device,'issue')); controls.append(issue);
  if (profiles.length) { const revoke=node('button','button secondary','Отозвать'); revoke.addEventListener('click',()=>confirmProfile(device,'revoke')); controls.append(revoke); }
  if (!state.vpn.available) card.append(node('p','small muted','Администратор должен выбрать готовый основной сервер.'));
  const stats=node('button','button secondary','Статистика');stats.addEventListener('click',()=>openDeviceTraffic(device.id));controls.append(stats);card.append(controls); container.append(card);
 }
}
async function loadServers() {
 [state.servers,state.jobs,state.sshKeys,state.monitors,state.health] = await Promise.all([api('/api/admin/servers'),api('/api/admin/jobs'),api('/api/admin/ssh-keys'),api('/api/admin/monitoring'),api('/api/health')]); renderSSHKeys(); const container = $('#servers'); container.replaceChildren(); $('#server-empty').hidden = state.servers.length > 0;
 for (const server of state.servers) {
  const card=node('article','server-card'); const top=node('div','card-top');
  const names={new:'Не развёрнут',checked:'SSH проверен',deploying:'Разворачивается',ready:'Развёрнут'};
  top.append(node('h3','',server.name),node('span','badge',server.is_default?'Основной сервер':(names[server.vpn_state]||server.vpn_state)));
  card.append(top,node('p','server-address',`${server.ssh_user}@${server.hostname}:${server.ssh_port}`));
  card.append(node('p','small muted',server.ssh_auth_method==='password'?'Вход по паролю':server.ssh_auth_method==='saved_key'?`SSH-ключ: ${state.sshKeys.find(k=>k.id===server.ssh_key_id)?.name||'Сохранённый'}`:'Отдельный SSH-ключ'));
  card.append(node('p','small muted',server.host_key_policy==='tofu'?'Ключ хоста запомнен при первом подключении':'Ключ хоста указан вручную'));
  const hostKey=node('details');hostKey.append(node('summary','','Отпечаток SSH-хоста'),node('code','',server.host_key_fingerprint));card.append(hostKey);
  if(server.endpoint) card.append(node('p','small muted',`${server.endpoint}:${server.vpn_port}/udp · AmneziaWG 3.1`));
  const job=state.jobs.find(j=>j.server_id===server.id);
  const busy=state.jobs.some(j=>j.server_id===server.id&&['queued','running'].includes(j.status));
  if(job) {const labels={queued:'В очереди',running:'Выполняется',succeeded:'Выполнено',failed:'Ошибка'};card.append(node('p','small muted',`${labels[job.status]} · попытка ${job.attempts}`));}
  if(server.vpn_error)card.append(node('p','form-error',server.vpn_error));
  const controls=node('div','card-actions');
  const check=node('button','button secondary','Проверить SSH');check.disabled=busy;check.addEventListener('click',()=>serverAction(server.id,'check'));controls.append(check);
  const deploy=node('button','button secondary',server.vpn_state==='ready'?'Повторить применение':'Развернуть');deploy.disabled=busy;deploy.addEventListener('click',()=>{
   state.deploying=server.id;const form=$('#deploy-form');form.elements.endpoint.value=server.endpoint||server.hostname;form.elements.port.value=server.vpn_port||51820;form.elements.endpoint.readOnly=!!server.endpoint;form.elements.port.readOnly=!!server.endpoint;openDialog('deploy');
  });controls.append(deploy);
  if(server.vpn_state==='ready'&&!server.is_default){const main=node('button','button primary','Сделать основным');main.addEventListener('click',()=>serverAction(server.id,'default'));controls.append(main);}
  const place=node('button','button secondary','Страна и город');place.addEventListener('click',()=>editLocation(server));controls.append(place,maintenanceButton(server));card.append(locationLabel(server),node('p','small',healthLabel(server.id)),node('p','small muted','Проверка с VPS: туннель, DNS и HTTPS. Доступ из сети устройства не проверяется.'),controls,serverMonitoring(server));container.append(card);
 }
}
async function loadEvents() {
 const events = await api('/api/admin/events'); const tbody = $('#events'); tbody.replaceChildren(); $('#event-empty').hidden = events.length > 0; $('#events-view .table-wrap').hidden = !events.length;
 for (const event of events) { const row = node('tr'); row.append(node('td', '', actions[event.action] || event.action), node('td', '', event.actor), node('td', '', new Date(event.created_at).toLocaleString('ru-RU'))); tbody.append(row); }
}
async function changeView(view) {
 if (!state.me || (!['devices','traffic','notifications'].includes(view) && state.me.user.role !== 'admin')) return;
 state.view = view;
 const title = { devices: 'Устройства', servers: 'Серверы', events: 'Журнал изменений', traffic:'Статистика',notifications:'Уведомления' }[view];
 $('#page-title').textContent = title; $('#breadcrumb').textContent = `Личный кабинет / ${title}`;
 $('#page-eyebrow').textContent = view === 'devices' ? 'ЛИЧНЫЙ КАБИНЕТ' : 'УПРАВЛЕНИЕ';
 $('#page-description').textContent = { devices: 'Отдельная запись для каждого подключения.', servers: 'Ваши VPS и параметры доступа.', events: 'Последние 100 изменений в Củ Chi.', traffic:'Трафик и недавние соединения ваших устройств.',notifications:'Состояние VPN и напоминания о профилях.' }[view];
 $('#add').hidden = ['events','traffic','notifications'].includes(view); $('#add').textContent = view === 'devices' ? '＋ Добавить устройство' : '＋ Добавить сервер';
 for (const name of ['devices', 'servers', 'events', 'traffic', 'notifications']) $(`#${name}-view`).hidden = name !== view;
 $$('[data-view]').forEach(el => el.classList.toggle('active', el.dataset.view === view));
 $('#page-error').hidden = true;
 state.loading=true;
 try { await ({ devices: loadDevices, servers: loadServers, events: loadEvents, traffic:loadTraffic,notifications:loadNotifications }[view])(); }
 catch (error) { $('#page-error').textContent = error.message; $('#page-error').hidden = false; } finally {state.loading=false;}
}
function openDialog(name) { const dialog = $(`#${name}-dialog`); $('.form-error', dialog).textContent = ''; dialog.showModal(); }
function bindForm(name, handler) {
 $(`#${name}-form`).addEventListener('submit', async event => {
  event.preventDefault(); const form = event.currentTarget; const submit = $('button[type="submit"]', form); submit.disabled = true; $('.form-error', form).textContent = '';
  try { await handler(form); } catch (error) { $('.form-error', form).textContent = error.message; } finally { submit.disabled = false; }
 });
}
$$('[data-close]').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
$$('dialog').forEach(dialog => dialog.addEventListener('close', () => { $$('textarea, input[type="password"]', dialog).forEach(el => { el.value = ''; }); }));
$$('[data-view]').forEach(button => button.addEventListener('click', () => changeView(button.dataset.view)));
$('#add').addEventListener('click', () => openDialog(state.view === 'devices' ? 'device' : 'server'));
$('#empty-add-device').addEventListener('click', () => openDialog('device')); $('#empty-add-server').addEventListener('click', () => openDialog('server'));
$('#logout').addEventListener('click', async () => { try { await api('/api/logout', { method: 'POST' }); state.me = null; location.reload(); } catch (error) { notify(error.message); } });
$('#dev-login').addEventListener('click', async event => { event.currentTarget.disabled = true; try { await api('/auth/dev', { method: 'POST' }); await start(); } catch (error) { $('#login-error').textContent = error.message; } finally { $('#dev-login').disabled = false; } });
bindForm('device', async form => { await api('/api/devices', { method: 'POST', body: JSON.stringify(Object.fromEntries(new FormData(form))) }); $('#device-dialog').close(); form.reset(); await loadDevices(); notify('Устройство добавлено'); });
bindForm('server', async form => { const data = Object.fromEntries(new FormData(form)); data.ssh_port = Number(data.ssh_port); await api('/api/admin/servers', { method: 'POST', body: JSON.stringify(data) }); $('#server-dialog').close(); form.reset(); updateSSHMethod(); updateHostPolicy(); await loadServers(); notify('Сервер сохранён'); });
bindForm('delete', async () => { await api(`/api/devices/${state.deleting}`, { method: 'DELETE' }); $('#delete-dialog').close(); state.deleting = null; await loadDevices(); notify('Устройство удалено'); });
function confirmProfile(device,action){
 state.profileAction={device,action}; $('#profile-title').textContent=action==='issue'?'Выпустить новый профиль?':'Отозвать профили?';
 $('#profile-description').textContent=action==='issue'?`Устройство «${device.name}», сервер «${state.vpn.server_name}». Старые ключи будут отозваны. После завершения импортируйте новый профиль в AmneziaWG; одно устройство — один профиль.`:`Ключи устройства «${device.name}» будут удалены со всех серверов. Недоступные серверы останутся в ожидании отзыва.`;
 openDialog('profile');
}
async function serverAction(id,action){try{await api(`/api/admin/servers/${id}/${action}`,{method:'POST'});await loadServers();notify(action==='default'?'Основной сервер изменён':'Задание поставлено в очередь');}catch(e){notify(e.message);}}
bindForm('deploy',async form=>{const input=Object.fromEntries(new FormData(form));input.port=Number(input.port);await api(`/api/admin/servers/${state.deploying}/deploy`,{method:'POST',body:JSON.stringify(input)});$('#deploy-dialog').close();await loadServers();notify('Деплой поставлен в очередь');});
bindForm('profile',async()=>{const {device,action}=state.profileAction;await api(`/api/devices/${device.id}/${action==='issue'?'profiles':'revoke'}`,{method:'POST'});$('#profile-dialog').close();await loadDevices();notify('Задание поставлено в очередь');});
$('#qr-dialog').addEventListener('close',()=>$('#profile-qr').removeAttribute('src'));
setInterval(async()=>{if(!state.me||state.loading||document.hidden||$('dialog[open]')||$('#servers details[open]'))return;state.loading=true;try{if(state.view==='servers')await loadServers();if(state.view==='devices')await loadDevices();if(state.view==='traffic')await loadTraffic();}catch(e){$('#page-error').textContent=e.message;$('#page-error').hidden=false;}finally{state.loading=false;}},5000);
function updateHostPolicy(){const field=$('#server-form').elements.host_key_fingerprint;const manual=$('#server-form').elements.host_key_policy.value==='manual';$('#host-fingerprint-field').hidden=!manual;$('#host-fingerprint-help').hidden=!manual;field.disabled=!manual;field.required=manual;if(!manual)field.value='';}
$('#server-form').elements.host_key_policy.addEventListener('change',updateHostPolicy);
function updateSSHMethod() {
 const method=$('#server-form').elements.ssh_auth_method.value;
 $$('[data-ssh-method]').forEach(label=>{const active=label.dataset.sshMethod===method;label.hidden=!active;const field=$('input,textarea,select',label);field.disabled=!active;field.required=active;if(!active&&field.tagName!=='SELECT')field.value='';});
}
$('#server-form').elements.ssh_auth_method.addEventListener('change',updateSSHMethod);
function updateKeyImport(){const label=$('#ssh-key-import');const active=$('#ssh-key-form').elements.mode.value==='import';label.hidden=!active;const field=$('textarea',label);field.disabled=!active;field.required=active;if(!active)field.value='';}
$('#ssh-key-form').elements.mode.addEventListener('change',updateKeyImport);
$('#add-ssh-key').addEventListener('click',()=>openDialog('ssh-key'));
bindForm('ssh-key',async form=>{await api('/api/admin/ssh-keys',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(form)))});$('#ssh-key-dialog').close();form.reset();updateKeyImport();await loadServers();notify('Ключ сохранён. Добавьте публичную часть на VPS.');});
function renderSSHKeys(){
 const signature=JSON.stringify(state.sshKeys);if(signature===renderedSSHKeys)return;renderedSSHKeys=signature;
 const select=$('#server-form').elements.ssh_key_id;const selected=select.value;select.replaceChildren();
 if(!state.sshKeys.length){const option=node('option','','Сначала добавьте SSH-ключ');option.value='';select.append(option);}
 const list=$('#ssh-keys');list.replaceChildren();
 for(const key of state.sshKeys){
  const option=node('option','',key.name);option.value=key.id;select.append(option);
  const card=node('article','server-card');card.append(node('h3','',key.name),node('p','small muted',`Серверов: ${key.server_count}`));
  const details=node('details');details.append(node('summary','','Публичный ключ для VPS'),node('code','',key.public_key),node('p','small muted',key.fingerprint));card.append(details);
  const controls=node('div','card-actions');const copy=node('button','button secondary','Копировать публичный ключ');copy.addEventListener('click',async()=>{try{await navigator.clipboard.writeText(key.public_key+'\n');notify('Публичный ключ скопирован');}catch{details.open=true;notify('Выделите и скопируйте публичный ключ');}});controls.append(copy);
  const remove=node('button','button secondary','Удалить ключ');remove.disabled=key.server_count>0;remove.addEventListener('click',async()=>{if(!confirm(`Удалить ключ «${key.name}» из Củ Chi? Его публичная часть на VPS останется.`))return;try{await api(`/api/admin/ssh-keys/${key.id}`,{method:'DELETE'});await loadServers();notify('Ключ удалён');}catch(e){notify(e.message);}});controls.append(remove);card.append(controls);list.append(card);
 }
 if(state.sshKeys.some(k=>k.id===selected))select.value=selected;
}
// Startup runs after monitoring.js has registered the additional views.
