'use strict';
// Only real interaction/foreground visits renew the idle timeout. API polling,
// push events and an unattended visible tab must not keep a session alive.
const recordSessionActivity=(()=>{
 const interval=30000;
 let pending=false,inFlight=false,timer=null,lastAttempt=-Infinity;
 function schedule(){
  if(inFlight||timer!==null||!pending)return;
  const delay=Math.max(0,lastAttempt+interval-Date.now());
  if(delay){timer=setTimeout(()=>{timer=null;flush();},delay);}else flush();
 }
 async function flush(){
  if(inFlight||!pending)return;
  if(document.hidden||!state.me){pending=false;return;}
  const account=state.me;pending=false;inFlight=true;lastAttempt=Date.now();
  try{
   const result=await api('/api/session/activity',{method:'POST'});
   if(state.me===account)account.expires_at=result.expires_at;
  }catch{
   // api() handles expiry. Other failures retry only on subsequent activity.
  }finally{
   inFlight=false;if(state.me!==account)pending=false;schedule();
  }
 }
 function activity(event){
  if(event&&!event.isTrusted||document.hidden||!state.me)return;
  pending=true;schedule();
 }
 for(const event of ['pointerdown','keydown','input','wheel','touchmove'])document.addEventListener(event,activity,{capture:true,passive:true});
 document.addEventListener('visibilitychange',event=>{
  if(document.hidden){pending=false;if(timer!==null)clearTimeout(timer);timer=null;}
  else activity(event);
 });
 window.addEventListener('popstate',activity);
 return activity;
})();
