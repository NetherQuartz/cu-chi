'use strict';
// No fetch handler and no caches: authenticated data and profiles stay network-only.
self.addEventListener('push',event=>{
 let data={};try{data=event.data?.json()||{};}catch{}
 const id=typeof data.id==='string'&&/^[a-f0-9-]{36}$/.test(data.id)?data.id:'notice';
 event.waitUntil(self.registration.showNotification(typeof data.title==='string'?data.title.slice(0,100):'Củ Chi',{
  body:typeof data.body==='string'?data.body.slice(0,400):'Откройте личный кабинет.',
  // Android masks the badge to its alpha silhouette; an opaque app tile becomes a square.
  icon:'/icon-192.png',badge:'/badge-96.png',tag:'cuchi-'+id,data:{id,view:data.kind==='xray_available'?'devices':'notifications'},
 }));
});
self.addEventListener('notificationclick',event=>{
 event.notification.close();
 event.waitUntil((async()=>{
  const url=new URL(event.notification.data?.view==='devices'?'/?view=devices':'/?view=notifications',self.location.origin).href;
  const windows=await self.clients.matchAll({type:'window',includeUncontrolled:true});
  for(const client of windows){if(new URL(client.url).origin===self.location.origin){await client.navigate(url);return client.focus();}}
  return self.clients.openWindow(url);
 })());
});
