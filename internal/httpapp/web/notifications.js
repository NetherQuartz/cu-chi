'use strict';
const noticeText={outage:'Проверка VPN несколько раз завершилась ошибкой. Администратор уведомлён.',management:'Не удаётся проверить сервер по SSH. Работа самого VPN не подтверждена и не опровергнута.',recovery:'Проверка туннеля, DNS и HTTPS снова проходит успешно.',reminder:'Доступен новый основной сервер. Перевыпустите профили устройств и импортируйте их в AmneziaWG.',test:'Тестовое уведомление Củ Chi.',job_failed:'Изменение VPN не удалось применить после повторных попыток. Проверьте задания сервера.'};
const noticeTitles={outage:'Проблема с VPN',management:'Сервер не удаётся проверить',recovery:'VPN восстановлен',reminder:'Пора обновить профили',test:'Проверка уведомлений',job_failed:'Ошибка применения'};
let pushRegistration=null,pushPublicKey='';
function healthLabel(id){
 const h=(state.health||[]).find(h=>h.server_id===id);if(!h)return 'Проверок туннеля ещё нет';
 if(h.maintenance_until&&new Date(h.maintenance_until)>new Date())return `Обслуживание до ${dateLabel(h.maintenance_until)}`;
 if(!h.checked_at)return 'Проверок туннеля ещё нет';
 if(Date.now()-new Date(h.checked_at).getTime()>900000)return 'Нет свежей проверки туннеля';
 const when=` · ${dateLabel(h.checked_at)}`;
 if(h.incident_id){if(h.result==='ok')return 'Проверка прошла, ждём повторного подтверждения'+when;return (h.incident_kind==='data'?'Есть проблема с VPN':'Нет доступа к управлению сервером')+when;}
 if(h.result==='ok')return 'Туннель, DNS и HTTPS работают'+when;
 if(h.result==='data')return 'Проверка VPN не прошла, перепроверяем'+when;
 return 'Не удалось проверить туннель'+when;
}
function maintenanceButton(server){
 const h=(state.health||[]).find(h=>h.server_id===server.id),active=h?.maintenance_until&&new Date(h.maintenance_until)>new Date();
 const b=node('button','button secondary',active?'Завершить обслуживание':'Обслуживание на час');
 b.addEventListener('click',async()=>{b.disabled=true;try{await api(`/api/admin/servers/${server.id}/maintenance`,{method:'POST',body:JSON.stringify({minutes:active?0:60})});await loadServers();}catch(e){notify(e.message);b.disabled=false;}});return b;
}
async function updateNoticeCount(){try{const items=await api('/api/notifications');const count=items.filter(x=>!x.read_at).length;$('#notice-count').textContent=count||'';return items;}catch{return [];}}
async function loadNotifications(){
 const items=await updateNoticeCount(),box=$('#notifications');box.replaceChildren();
 if(!items.length)box.append(node('div','empty-state','Уведомлений пока нет'));
 for(const n of items){const card=node('article',`server-card notice-card ${n.read_at?'':'unread'}`);const top=node('div','card-top');top.append(node('h2','',noticeTitles[n.kind]||'Уведомление'),node('span',`badge ${n.severity==='critical'?'critical':''}`,{critical:'Критично',warning:'Внимание',info:'Информация'}[n.severity]));card.append(top,node('p','',noticeText[n.kind]||''));if(n.server_name)card.append(node('p','small muted',n.server_name));card.append(node('p','small muted',dateLabel(n.created_at)));const controls=node('div','card-actions');const open=node('button','button secondary',['management','job_failed'].includes(n.kind)?'Открыть серверы':'Открыть устройства');open.addEventListener('click',()=>changeView(['management','job_failed'].includes(n.kind)?'servers':'devices'));controls.append(open);if(!n.read_at){const read=node('button','button secondary','Прочитано');read.addEventListener('click',async()=>{try{await api(`/api/notifications/${n.id}/read`,{method:'POST'});await loadNotifications();}catch(e){notify(e.message);}});controls.append(read);}card.append(controls);box.append(card);}
 const config=await api('/api/push');pushPublicKey=config.public_key;
 for(const [key,value]of Object.entries(config.preferences))$('#push-preferences').elements[key].checked=value;
 await showPushState();
}
async function getPushRegistration(){if(!('serviceWorker'in navigator)||!('PushManager'in window))return null;if(!pushRegistration)pushRegistration=await navigator.serviceWorker.register('/sw.js',{scope:'/'});return navigator.serviceWorker.ready;}
async function showPushState(){
 const supported='serviceWorker'in navigator&&'PushManager'in window&&'Notification'in window;
 const enabled=$('#enable-push'),disabled=$('#disable-push'),test=$('#test-push');
 enabled.disabled=!supported;disabled.hidden=true;test.hidden=true;
 if(!supported){$('#push-status').textContent='Этот браузер не поддерживает Web Push. На iPhone добавьте Củ Chi на экран «Домой» и откройте оттуда. История уведомлений доступна здесь.';return;}
 const reg=await getPushRegistration(),sub=await reg.pushManager.getSubscription();
 enabled.hidden=!!sub;disabled.hidden=!sub;test.hidden=!sub;
 $('#push-status').textContent=sub?'Подписка есть в этом браузере. Сохраните её кнопкой «Подключить заново», если восстанавливали базу.':Notification.permission==='denied'?'Уведомления запрещены в настройках браузера. Измените разрешение для этого сайта, затем подключите снова.':'Уведомления отключены. Подключите их отдельно на каждом телефоне или компьютере.';
 $('#renew-push').hidden=!sub;
 await refreshPushDelivery();
}
async function refreshPushDelivery(){
 const reg=await getPushRegistration(),sub=await reg?.pushManager.getSubscription();if(!sub){$('#push-test-status').textContent='';return;}
 const result=await api('/api/push/status',{method:'POST',body:JSON.stringify({endpoint:sub.endpoint})});
 $('#test-push').disabled=!result.registered;
 if(!result.registered){$('#push-test-status').textContent='Подписки нет на сервере или она истекла. Нажмите «Подключить заново».';return;}
 const labels={pending:'Тест в очереди',sending:'Отправляем тест',accepted:'Push-сервис принял тест. Это не подтверждает показ на устройстве.',failed:'Push-сервис не принял тест. Проверьте подписку и сеть NAS.',skipped:'Тест пропущен: истёк, прочитан или больше не актуален.',expired:'Время отправки теста истекло. Повторите проверку.'};
 $('#push-test-status').textContent=result.status?`${labels[result.status]||result.status} · ${dateLabel(result.at)}`:'Тест ещё не отправлялся.';
}
setInterval(()=>{if(state.me&&state.view==='notifications'&&!document.hidden)refreshPushDelivery().catch(()=>{});},5000);
function applicationKey(v){const raw=atob(v.replace(/-/g,'+').replace(/_/g,'/'));return Uint8Array.from(raw,c=>c.charCodeAt(0));}
async function enablePush(){
 // Permission must be requested directly from this click, before network awaits (iOS).
 const permission=await Notification.requestPermission();if(permission!=='granted'){await showPushState();return;}
 const reg=await getPushRegistration();let sub=await reg.pushManager.getSubscription();
 if(!sub)sub=await reg.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:applicationKey(pushPublicKey)});
 try{await api('/api/push/subscription',{method:'POST',body:JSON.stringify(sub.toJSON())});}catch(e){throw new Error(e.message+' Если браузер подключён к другому аккаунту, отключите его там.');}
 await showPushState();notify('Подписка сохранена. Можно отправить тест.');
}
async function disablePush(){const reg=await getPushRegistration(),sub=await reg?.pushManager.getSubscription();if(sub){await api('/api/push/subscription',{method:'DELETE',body:JSON.stringify({endpoint:sub.endpoint})});await sub.unsubscribe();}await showPushState();}
for(const id of ['enable-push','renew-push'])$('#'+id).addEventListener('click',()=>enablePush().catch(e=>notify(e.message)));
$('#disable-push').addEventListener('click',()=>disablePush().catch(e=>notify(e.message)));
$('#test-push').addEventListener('click',async()=>{try{const reg=await getPushRegistration(),sub=await reg?.pushManager.getSubscription();if(!sub)throw new Error('Сначала подключите этот браузер');await api('/api/push/test',{method:'POST',body:JSON.stringify({endpoint:sub.endpoint})});notify('Тест поставлен в очередь. Это ещё не подтверждение доставки.');await loadNotifications();}catch(e){notify(e.message);}});
$('#push-preferences').addEventListener('submit',async e=>{e.preventDefault();const p={};for(const key of ['outages','recovery','reminders','operations'])p[key]=e.target.elements[key].checked;try{await api('/api/push/preferences',{method:'PUT',body:JSON.stringify(p)});notify('Настройки уведомлений сохранены');}catch(err){notify(err.message);}});
$('#preview-reminders').addEventListener('click',async()=>{try{const users=await api('/api/admin/reminders');$('#reminder-audience').textContent=users.length?users.map(u=>u.username).join(', '):'Все действующие профили используют основной сервер.';$('#send-reminders').hidden=!users.length;$('#send-reminders').textContent=`Напомнить пользователям (${users.length})`;}catch(e){notify(e.message);}});
$('#send-reminders').addEventListener('click',async()=>{const b=$('#send-reminders');b.disabled=true;try{const result=await api('/api/admin/reminders',{method:'POST'});notify(`Создано напоминаний: ${result.created}. Повторные за сегодня пропущены.`);b.hidden=true;await loadNotifications();}catch(e){notify(e.message);}finally{b.disabled=false;}});
setInterval(()=>{if(state.me&&!document.hidden)updateNoticeCount();},60000);
loadCountries().catch(()=>{}).finally(()=>start());
