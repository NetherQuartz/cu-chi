'use strict';
let egressSelection = null;
function egressError(code) {
 return ({warp_registration_failed:'Не удалось зарегистрировать WARP. Проверьте доступ VPS к Cloudflare и повторите применение.',warp_configuration_invalid:'Cloudflare вернул неподдерживаемую конфигурацию WARP. Переключение остановлено.',warp_egress_failed:'Проверка выхода через WARP не прошла. Запрошен возврат к прежнему режиму; проверьте состояние сервера.',egress_rollback_failed:'Не удалось восстановить прежний выход. Нужна проверка сервера; выберите прямой выход и повторите применение.',wireguard_module_missing:'В ядре VPS недоступен обычный WireGuard, необходимый для WARP.'})[code]||code;
}
function egressControl(server,busy) {
 const box=node('div','egress-control');
 const name=mode=>mode==='warp'?'WARP · экспериментально':'Напрямую с VPS';
 const desired=server.egress_mode||'direct', applied=server.applied_egress_mode||'direct';
 box.append(node('strong','','Выход в интернет'),node('p','small',`Последний успешно применённый: ${name(applied)}`));
 if(desired!==applied)box.append(node('p','small muted',`Запрошен: ${name(desired)}${busy?' · применяется':' · не подтверждён'}`));
 if(server.vpn_state!=='ready'){box.append(node('p','small muted','Выбор выхода доступен после развёртывания сервера.'));return box;}
 const controls=node('div','card-actions');
 for(const [mode,label] of [['direct','Прямой выход'],['warp','Выход через WARP']]){
  const button=node('button','button secondary',label);
  button.disabled=busy||(mode===desired&&mode===applied&&!server.vpn_error);
  button.addEventListener('click',()=>{
   egressSelection={id:server.id,mode};
   $('#egress-title').textContent=mode==='warp'?'Включить WARP?':'Включить прямой выход?';
   $('#egress-description').textContent=`Сервер «${server.name}». ${mode==='warp'?'VPN-трафик будет выходить в интернет через Cloudflare.':'VPN-трафик будет выходить в интернет с IP этого VPS.'}`;
   $('#egress-warp-help').hidden=mode!=='warp';
   const check=$('#egress-form').elements.accept_terms;check.checked=false;check.required=mode==='warp';check.disabled=mode!=='warp';
   openDialog('egress');
  });controls.append(button);
 }
 box.append(controls);return box;
}
bindForm('egress',async form=>{
 const selected=egressSelection;
 await api(`/api/admin/servers/${selected.id}/egress`,{method:'POST',body:JSON.stringify({mode:selected.mode,accept_terms:form.elements.accept_terms.checked})});
 $('#egress-dialog').close();await loadServers();notify('Переключение выхода поставлено в очередь. Профили менять не нужно.');
});
