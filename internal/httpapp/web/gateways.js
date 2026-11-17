'use strict';
Object.assign(actions, {'gateway.registered':'Шлюз добавлен','gateway.check_queued':'Запрошена проверка шлюза','gateway.deploy_queued':'Запрошено развёртывание шлюза'});
async function loadGateways(){
 const [gateways,keys,profiles,vpn]=await Promise.all([api('/api/admin/gateways'),api('/api/admin/ssh-keys'),api('/api/profiles'),api('/api/vpn')]);
 const select=$('#gateway-form').elements.ssh_key_id, selected=select.value;select.replaceChildren();
 if(!keys.length){const o=node('option','','Добавьте ключ в разделе «Серверы»');o.value='';select.append(o);}
 for(const k of keys){const o=node('option','',k.name);o.value=k.id;select.append(o);}if(keys.some(k=>k.id===selected))select.value=selected;
 const list=$('#gateways');list.replaceChildren();$('#gateway-empty').hidden=gateways.length>0;
 for(const g of gateways){
  const card=node('article','server-card'),top=node('div','card-top');
  const labels={new:'Не настроен',queued:'В очереди',running:'Выполняется',checked:'SSH проверен',ready:'Применён',failed:'Ошибка'};
  top.append(node('h3','',g.name),node('span','badge',labels[g.state]||g.state));card.append(top,node('p','server-address',`${g.ssh_user}@${g.hostname}:${g.ssh_port} · Keenetic ${g.router_ip}`));
  const details=node('details');details.append(node('summary','','Отпечаток SSH-хоста'),node('code','',g.host_key_fingerprint));card.append(details);
  const current=profiles.find(p=>p.device_id===g.device_id&&p.desired),busy=['queued','running'].includes(g.state);
  if(current){card.append(node('p','small',`${current.server_name} · профиль ${current.revision} · ${current.state==='ready'?'готов на VPS':'применяется на VPS'}`));if(current.server_id!==vpn.server_id)card.append(node('p','form-error','Основной VPS изменился: выпустите новый профиль, затем примените его на VM.'));}
  else card.append(node('p','small muted','Сначала выпустите отдельный профиль шлюза на основном VPS.'));
  if(g.applied_profile_id && g.applied_profile_id!==g.current_profile_id)card.append(node('p','form-error','На VM остался прежний профиль. Примените текущий профиль после его готовности.'));
  if(g.applied_at)card.append(node('p','small muted',`Туннель, DNS и HTTPS проверены при последнем успешном применении: ${dateLabel(g.applied_at)}. Это не текущий статус Keenetic.`));
  if(g.error)card.append(node('p','form-error',g.error));
  if(busy)card.append(node('p','small muted',`Попытка ${g.attempts} из 3`));
  const controls=node('div','card-actions');
  const check=node('button','button secondary','Проверить SSH');check.disabled=busy;check.addEventListener('click',()=>gatewayAction(g.id,'check'));controls.append(check);
  const issue=node('button','button secondary',current?'Обновить профиль VPS':'Выпустить профиль VPS');issue.disabled=busy||!vpn.available||current?.state==='pending';
  issue.addEventListener('click',async()=>{if(current&&!confirm('Выпустить новый профиль шлюза? Старый будет отозван. После готовности примените новый профиль на VM; до этого VPN на Keenetic прервётся.'))return;issue.disabled=true;try{await api(`/api/devices/${g.device_id}/profiles`,{method:'POST'});await loadGateways();notify('Профиль выпускается на VPS');}catch(e){notify(e.message);}finally{issue.disabled=false;}});controls.append(issue);
  const deploy=node('button','button primary',g.applied_at?'Применить профиль на VM':'Развернуть на VM');deploy.disabled=busy||current?.state!=='ready';deploy.addEventListener('click',()=>gatewayAction(g.id,'deploy'));controls.append(deploy);
  const download=node('a','button secondary','Профиль для Keenetic');download.href=`/api/admin/gateways/${g.id}/keenetic.conf`;controls.append(download);card.append(controls,node('p','small muted','Профиль Keenetic импортируется один раз. При смене VPS обновляется только VM. Трафик шлюза виден в разделе «Статистика».'));list.append(card);
 }
}
async function gatewayAction(id,action){try{await api(`/api/admin/gateways/${id}/${action}`,{method:'POST'});await loadGateways();notify('Задание поставлено в очередь');}catch(e){notify(e.message);}}
function gatewayAuth(){
 const form=$('#gateway-form');for(const label of $$('[data-gateway-auth]',form)){const active=label.dataset.gatewayAuth===form.elements.ssh_auth_method.value;label.hidden=!active;const field=$('input,select,textarea',label);field.disabled=!active;field.required=active;if(!active&&field.tagName!=='SELECT')field.value='';}
 const manual=form.elements.host_key_policy.value==='manual';$('#gateway-fingerprint').hidden=!manual;form.elements.host_key_fingerprint.disabled=!manual;form.elements.host_key_fingerprint.required=manual;if(!manual)form.elements.host_key_fingerprint.value='';
}
$('#gateway-form').elements.ssh_auth_method.addEventListener('change',gatewayAuth);
$('#gateway-form').elements.host_key_policy.addEventListener('change',gatewayAuth);
bindForm('gateway',async form=>{const data=Object.fromEntries(new FormData(form));data.ssh_port=Number(data.ssh_port);await api('/api/admin/gateways',{method:'POST',body:JSON.stringify(data)});$('#gateway-dialog').close();form.reset();gatewayAuth();await loadGateways();notify('Шлюз и его устройство добавлены');});
