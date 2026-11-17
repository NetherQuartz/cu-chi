'use strict';
// No fetch handler and no caches: authenticated data and profiles stay network-only.
self.addEventListener('push',event=>{
 let data={};try{data=event.data?.json()||{};}catch{}
 const id=typeof data.id==='string'&&/^[a-f0-9-]{36}$/.test(data.id)?data.id:'notice';
 event.waitUntil(self.registration.showNotification(typeof data.title==='string'?data.title.slice(0,100):'Củ Chi',{
  body:typeof data.body==='string'?data.body.slice(0,400):'Откройте личный кабинет.',
  icon:'/icon-192.png',badge:'/icon-192.png',tag:'cuchi-'+id,data:{id},
 }));
});
self.addEventListener('notificationclick',event=>{
 event.notification.close();
 event.waitUntil((async()=>{
  const url=new URL('/?view=notifications',self.location.origin).href;
  const windows=await self.clients.matchAll({type:'window',includeUncontrolled:true});
  for(const client of windows){if(new URL(client.url).origin===self.location.origin){await client.navigate(url);return client.focus();}}
  return self.clients.openWindow(url);
 })());
});
