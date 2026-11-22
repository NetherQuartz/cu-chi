'use strict';
function xrayButton(label,action) {
 const button=node('button','button secondary',label);
 button.addEventListener('click',async()=>{
  button.disabled=true;
  try { await action(); } catch(e) { notify(e.message); } finally { button.disabled=false; }
 });
 return button;
}
function renderXrayServer(card,server) {
 const box=node('div','egress-control'), n=state.xrayNodes.find(x=>x.id===server.id);
 box.append(node('strong','','Xray · VLESS + REALITY'));
 if(n) {
  const pending=n.applied<n.revision;
  box.append(node('p',n.error?'form-error':'small muted',n.error?`Не удалось применить: ${n.error}. Повтор через 3 минуты.`:pending?'Ожидается применение…':`Готов · TCP/443 · SNI ${n.sni} · прямой выход`));
  box.append(node('p','small muted',n.published?'Включён в подписки устройств':'Скрыт из подписок устройств'));
 }
 const apply=async(published)=>{
  const notice=n?'':published?' После успешной проверки пользователи получат уведомление.':' Сервер будет скрыт до публикации.';
  if(!confirm(`Развернуть Xray на «${server.name}»? Нужен свободный TCP-порт 443.${notice}`))return;
  await api(`/api/admin/servers/${server.id}/xray`,{method:'POST',body:JSON.stringify({published})});
  await loadServers();
 };
 const deploy=xrayButton(n?'Повторить применение Xray':'Развернуть Xray',()=>apply(true));
 deploy.disabled=!!n&&n.applied<n.revision&&!n.error;
 box.append(deploy);
 if(!n)box.append(xrayButton('Развернуть без публикации',()=>apply(false)));
 if(n)box.append(xrayButton(n.published?'Скрыть из подписок':'Добавить в подписки',async()=>{
  await api(`/api/admin/servers/${server.id}/xray-publication`,{method:'POST',body:JSON.stringify({published:!n.published})});await loadServers();
 }));
 card.append(box);
}
function renderXrayDevice(card,device) {
 const {devices,servers}=state.xray;
 const grant=devices.find(d=>d.device_id===device.id);
 const history=state.xray.archived_servers[device.id]||[];
 if(!servers.length&&!grant&&!history.length)return;
 const box=node('div','egress-control'),path=`/api/devices/${device.id}/xray`;
 box.append(node('strong','','Xray · VLESS'));
 if(history.length)box.append(node('p','small muted',`Прежние серверы: ${history.join(', ')}`));
 if(!grant && !servers.length){card.append(box);return;}
 if(!grant)box.append(xrayButton('Выпустить Xray',async()=>{
  await api(path,{method:'POST'});await loadDevices();
 }));
 else if(!grant.desired)box.append(node('p','small muted','Ожидается подтверждение отзыва от серверов'));
 else {
  if(servers.some(n=>n.applied>=grant.revision))box.append(xrayButton('Подключить Xray',async()=>{
   const data=await api(path+'/import');
   $('#xray-url').value=data.subscription;$('#xray-links').value=data.links.join('\n');$('#xray-qr').src=path+'/import?qr=1';$('#xray-import').showModal();
  }));
  else box.append(node('p','small muted',servers.length?'Подписка появится после применения на сервере':'Доступных серверов Xray пока нет'));
  box.append(xrayButton('Отозвать Xray',async()=>{
   if(confirm(`Отозвать доступ Xray для «${device.name}» на всех серверах?`)){await api(path,{method:'DELETE'});await loadDevices();}
  }));
 }
 card.append(box);
}
$('#copy-xray').addEventListener('click',async()=>{
 try {await navigator.clipboard.writeText($('#xray-url').value);notify('Подписка скопирована');}
 catch {$('#xray-url').select();}
});
$('#xray-import').addEventListener('close',()=>{
 $('#xray-url').value='';$('#xray-links').value='';$('#xray-qr').removeAttribute('src');
});
