'use strict';
(() => {
 const $=s=>document.querySelector(s), list=$('#admin-list'), groupList=$('#groups-list');
 let sounds=[],groups=[],activeSound=null,activeGroup=null,pendingImport=false,nick='',toastTimer;
 let preview=null,previewButton=null;
 let adminSocket=null, adminReconnect=null, adminLeaving=false, adminBackoff=750;
 let participantID='';
 try {nick=localStorage.getItem('soundboard.nickname')||''}catch{}
 const collator=new Intl.Collator('de',{numeric:true,sensitivity:'base'});
 try{participantID=localStorage.getItem('soundboard.participantId')||''}catch{}
 if(!/^[a-zA-Z0-9-]{8,80}$/.test(participantID)){
   participantID=(typeof crypto!=='undefined'&&crypto.randomUUID)?crypto.randomUUID():`local-${Date.now()}-${Math.floor(Math.random()*1e9)}`;
   try{localStorage.setItem('soundboard.participantId',participantID)}catch{}
 }
 function announceAdminIdentity(){
   if(adminSocket?.readyState===WebSocket.OPEN && nick){
     adminSocket.send(JSON.stringify({type:'set_name',name:nick,participantId:participantID}));
   }
 }
 function connectAdminPresence(){
   if(adminLeaving || adminSocket?.readyState<=WebSocket.OPEN)return;
   const proto=location.protocol==='https:'?'wss:':'ws:';
   const ws=new WebSocket(`${proto}//${location.host}/admin/ws`);
   adminSocket=ws;
   ws.addEventListener('open',()=>{
     if(ws!==adminSocket)return;
     adminBackoff=750;
     $('#admin-mode-state').textContent='Admin-Modus aktiv · globale Sounds auf diesem Gerät stumm';
     announceAdminIdentity();
   });
   ws.addEventListener('close',()=>{
     if(ws!==adminSocket)return;
     $('#admin-mode-state').textContent='Admin-Status nicht verbunden · Zugriff auf /admin/ws prüfen';
     if(!adminLeaving){
       clearTimeout(adminReconnect);
       adminReconnect=setTimeout(connectAdminPresence,adminBackoff);
       adminBackoff=Math.min(10000,Math.round(adminBackoff*1.7));
     }
   });
   ws.addEventListener('error',()=>ws.close());
 }
 window.addEventListener('pagehide',()=>{
   adminLeaving=true;clearTimeout(adminReconnect);
   adminSocket?.close();adminSocket=null;
 });
 window.addEventListener('pageshow',()=>{
   if(adminLeaving){adminLeaving=false;connectAdminPresence()}
 });
 const colorForGroup=name=>groups.find(x=>x.name===name)?.color||'#8492a9';
 function notice(message){const el=$('#admin-toast');el.textContent=message;el.classList.add('show');clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.classList.remove('show'),3000)}
 async function api(path,options={}){const res=await fetch(path,{...options,headers:{'X-Soundboard-Nickname':nick,...(options.headers||{})}});const json=await res.json().catch(()=>({error:'Ungültige Serverantwort'}));if(!res.ok){const err=new Error(json.error||`HTTP ${res.status}`);err.status=res.status;err.data=json;throw err}return json}
 const soundPath=s=>`/api/admin/sounds/${encodeURIComponent(s.filename)}`;
 const groupPath=g=>`/api/admin/groups/${encodeURIComponent(g.name)}`;
 const close=id=>$('#'+id).close();
 document.querySelectorAll('[data-close]').forEach(b=>b.addEventListener('click',()=>close(b.dataset.close)));
 // Use precisely the same audio creation, original-file URL and lifecycle as
 // the normal soundboard; the preview never sends a WebSocket play event.
 const previewPlayer=SoundboardAudio.create();
 let previewGeneration=0;
 function stopPreview(){
   previewGeneration++;
   preview=null;
   if(previewButton){
     previewButton.textContent='▶';
     previewButton.classList.remove('is-previewing');
     previewButton.setAttribute('aria-pressed','false');
     previewButton.setAttribute('aria-label',previewButton.dataset.previewLabel);
     previewButton=null;
   }
   SoundboardAudio.stop(previewPlayer);
 }
 function togglePreview(sound,button){
   if(previewButton===button){stopPreview();return}
   stopPreview();
   preview=sound;
   previewButton=button;
   const generation=previewGeneration;
   button.textContent='■';
   button.classList.add('is-previewing');
   button.setAttribute('aria-pressed','true');
   button.setAttribute('aria-label',`${sound.name} stoppen`);
   // Invoke play() synchronously in the user's click handler to preserve
   // autoplay activation, just as the player UI does.
   SoundboardAudio.play(previewPlayer,sound).catch(error=>{
     if(generation!==previewGeneration || error?.name==='AbortError')return;
     stopPreview();
     if(error?.name==='NotAllowedError')notice('Audio durch Browser blockiert. Bitte Wiedergabe erlauben.');
     else notice(`Audio konnte nicht abgespielt werden: ${sound.name}`);
   });
 }
 previewPlayer.addEventListener('ended',()=>{if(preview)stopPreview()});
 window.addEventListener('pagehide',()=>stopPreview());
 async function load(){const out=await api('/api/admin/sounds');stopPreview();sounds=out.sounds||[];groups=out.groups||[];render()}
 function selectOptions(el,value='default'){el.replaceChildren();for(const g of groups){const o=document.createElement('option');o.value=g.name;o.textContent=g.name;el.append(o)}el.value=value;if(!el.value&&groups.length)el.value='default'}
 function render(){
  $('#total-count').textContent=`${sounds.length} Sounds`;$('#groups-count').textContent=`${groups.length} Gruppen`;
  selectOptions($('#upload-group'),$('#upload-group').value||'default');selectOptions($('#edit-group'),$('#edit-group').value||'default');
  const oldPlaying=preview?.sound?.filename;
  list.replaceChildren();groupList.replaceChildren();
  for(const s of [...sounds].sort((a,b)=>collator.compare(a.name,b.name))){
   const row=document.createElement('div');row.className='admin-row';
   const dot=document.createElement('span');dot.className='admin-avatar';dot.style.background=colorForGroup(s.group);
   const main=document.createElement('div');main.className='admin-row-main';
   const title=document.createElement('strong');title.textContent=s.name;
   const detail=document.createElement('small');detail.textContent=`${s.filename} · ${s.group} · ${(s.durationMs/1000).toFixed(1)} s`;
   const attribution=document.createElement('small');attribution.textContent=s.uploadedBy?`Hochgeladen von ${s.uploadedBy}${s.uploadedAt?' · '+new Date(s.uploadedAt).toLocaleString('de-DE'):''}`:'Manuell hinzugefügt / Uploader unbekannt';
   main.append(title,detail,attribution);
   const actions=document.createElement('div');actions.className='admin-row-actions';
   const play=document.createElement('button');play.className='action-button';play.type='button';play.textContent='▶';play.title=`${s.name} nur hier probehören`;play.dataset.previewLabel=`${s.name} probehören`;play.setAttribute('aria-label',play.dataset.previewLabel);play.setAttribute('aria-pressed','false');play.addEventListener('click',()=>togglePreview(s,play));
   const edit=document.createElement('button');edit.className='action-button';edit.type='button';edit.textContent='Bearbeiten';edit.addEventListener('click',()=>openEdit(s));
   const del=document.createElement('button');del.className='action-button';del.type='button';del.textContent='Löschen';del.addEventListener('click',()=>openDelete(s));
   actions.append(play,edit,del);row.append(dot,main,actions);list.append(row);
  }
  if(!sounds.length){const p=document.createElement('p');p.className='muted';p.textContent='Noch keine Sounds. Datei hochladen oder einen Resync auslösen.';list.append(p)}
  for(const g of groups){
   const row=document.createElement('div');row.className='admin-row';const dot=document.createElement('span');dot.className='admin-avatar';dot.style.background=g.color;
   const main=document.createElement('div');main.className='admin-row-main';const title=document.createElement('strong');title.textContent=g.name;
   const sub=document.createElement('small');sub.textContent=`${g.count} Sounds · ${g.color}${g.name==='default'?' · Fallback für Sounds ohne Gruppenzuordnung':''}`;main.append(title,sub);
   const actions=document.createElement('div');actions.className='admin-row-actions';
   if(g.name==='default'){const note=document.createElement('small');note.textContent='Standard · nicht löschbar';actions.append(note)}
   else{const edit=document.createElement('button');edit.type='button';edit.className='action-button';edit.textContent='Bearbeiten';edit.addEventListener('click',()=>openGroup(g));const del=document.createElement('button');del.type='button';del.className='action-button';del.textContent='Löschen';del.addEventListener('click',()=>deleteGroup(g));actions.append(edit,del)}
   row.append(dot,main,actions);groupList.append(row)
  }
  if(oldPlaying && !sounds.some(s=>s.filename===oldPlaying))stopPreview();
 }
 function showTab(tab){const isGroup=tab==='groups';$('#sounds-section').hidden=isGroup;$('#groups-section').hidden=!isGroup;for(const t of ['sounds','groups']){$('#tab-'+t).classList.toggle('active',t===tab);if(t===tab)$('#tab-'+t).setAttribute('aria-current','page');else $('#tab-'+t).removeAttribute('aria-current')}}
 $('#tab-sounds').addEventListener('click',()=>showTab('sounds'));$('#tab-groups').addEventListener('click',()=>showTab('groups'));
 const importForm=$('#import-form');$('#import-open').addEventListener('click',()=>{$('#import-error').hidden=true;selectOptions($('#upload-group'),'default');$('#import-dialog').showModal()});
 importForm.querySelectorAll('[name=method]').forEach(r=>r.addEventListener('change',()=>{const url=importForm.querySelector('input[name=method]:checked').value==='url';$('#url-fields').hidden=!url;$('#file-fields').hidden=url}));
 function buildImport(policy){const name=$('#upload-name').value.trim(),group=$('#upload-group').value;
  if(importForm.querySelector('input[name=method]:checked').value==='url')return {path:'/api/admin/import-url',options:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:$('#upload-url').value.trim(),name,group,onConflict:policy})}};
  const form=new FormData();const files=$('#upload-files').files;if(!files.length)throw Error('Bitte Audiodatei auswählen');for(const f of files)form.append('files',f);form.append('name',files.length===1?name:'');form.append('group',group);form.append('onConflict',policy);return {path:'/api/admin/upload',options:{method:'POST',body:form}}
 }
 async function submitImport(policy=''){const btn=$('#import-submit');btn.disabled=true;$('#import-error').hidden=true;try{const request=buildImport(policy);const result=await api(request.path,request.options);close('import-dialog');importForm.reset();$('#file-fields').hidden=false;$('#url-fields').hidden=true;stopPreview();await load();notice(`${result.imported} Sound(s) importiert`)}catch(e){if(e.status===409){$('#conflict-files').textContent=`Betroffene Dateien: ${(e.data.conflicts||[]).join(', ')}. Entscheidung gilt für alle Konflikte.`;pendingImport=true;$('#conflict-dialog').showModal()}else{$('#import-error').textContent=e.message;$('#import-error').hidden=false}}finally{btn.disabled=false}}
 importForm.addEventListener('submit',e=>{e.preventDefault();submitImport()});
 $('#conflict-dialog').addEventListener('close',()=>{if($('#conflict-dialog').returnValue==='confirm'&&pendingImport)submitImport($('#conflict-form').elements.resolution.value);pendingImport=false});
 function openEdit(s){activeSound=s;$('#edit-file').textContent=s.filename;$('#edit-name').value=s.name===s.filename.replace(/\.[^.]+$/,'')?'':s.name;selectOptions($('#edit-group'),s.group);$('#edit-error').hidden=true;$('#edit-dialog').showModal()}
 $('#edit-form').addEventListener('submit',async e=>{e.preventDefault();if(!activeSound)return;try{await api(soundPath(activeSound),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:$('#edit-name').value,group:$('#edit-group').value})});close('edit-dialog');await load();notice('Sound aktualisiert')}catch(err){$('#edit-error').textContent=err.message;$('#edit-error').hidden=false}});
 function openDelete(s){activeSound=s;$('#delete-file').textContent=`${s.name} (${s.filename})`;$('#delete-dialog').showModal()}
 $('#delete-dialog').addEventListener('close',async()=>{if($('#delete-dialog').returnValue!=='confirm'||!activeSound)return;try{if(preview)stopPreview();await api(soundPath(activeSound),{method:'DELETE'});await load();notice('Sound gelöscht')}catch(e){notice(e.message)}});
 $('#resync').addEventListener('click',async()=>{const btn=$('#resync');btn.disabled=true;$('#sync-status').textContent='Synchronisiere …';try{const r=await api('/api/admin/resync',{method:'POST'});stopPreview();await load();$('#sync-status').textContent=`${r.added} neu · ${r.updated} geändert · ${r.removed} entfernt`;notice('Bibliothek synchronisiert')}catch(e){$('#sync-status').textContent=e.message;notice('Resync fehlgeschlagen')}finally{btn.disabled=false}});
 async function suggestGroupColor(){
   const current=$('#group-color').value;
   const response=await api('/api/admin/groups/suggest-color?exclude='+encodeURIComponent(current));
   $('#group-color').value=response.color;
   $('#group-color-code').textContent=response.color;
 }
 async function openGroup(g){
   activeGroup=g||null;
   $('#group-heading').textContent=g?'Gruppe bearbeiten':'Gruppe erstellen';
   $('#group-name').value=g?.name||'';
   $('#group-color').value=g?.color||'#517dd0';
   $('#group-color-code').textContent=$('#group-color').value;
   $('#group-error').hidden=true;
   $('#group-dialog').showModal();$('#group-name').focus();
   if(!g){try{await suggestGroupColor()}catch(e){notice('Farbvorschlag nicht verfügbar: '+e.message)}}
 }
 $('#group-random-color').addEventListener('click',async()=>{
   try{await suggestGroupColor()}catch(e){notice('Farbvorschlag nicht verfügbar: '+e.message)}
 });
 $('#group-color').addEventListener('input',()=>$('#group-color-code').textContent=$('#group-color').value);
 $('#group-add').addEventListener('click',()=>openGroup(null));
 $('#group-form').addEventListener('submit',async e=>{e.preventDefault();const group=activeGroup;const name=$('#group-name').value.trim(),color=$('#group-color').value;try{await api(group?groupPath(group):'/api/admin/groups',{method:group?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name,color})});close('group-dialog');await load();notice(group?'Gruppe aktualisiert':'Gruppe erstellt')}catch(err){$('#group-error').textContent=err.message;$('#group-error').hidden=false}});
 function deleteGroup(g){activeGroup=g;$('#group-delete-message').textContent=`„${g.name}“ löschen? ${g.count} Sounds werden zur Gruppe „default“ verschoben.`;$('#group-delete-dialog').showModal()}
 $('#group-delete-dialog').addEventListener('close',async()=>{if($('#group-delete-dialog').returnValue!=='confirm'||!activeGroup)return;try{await api(groupPath(activeGroup),{method:'DELETE'});await load();notice('Gruppe gelöscht')}catch(err){notice(err.message)}});
 function openNickDialog(edit=false){$('#admin-nick-input').value=nick;$('#admin-nick-error').hidden=true;$('#admin-nick-cancel').hidden=!edit;$('#admin-nick-dialog').showModal();$('#admin-nick-input').focus()}
 $('#admin-name').addEventListener('click',()=>openNickDialog(true));$('#admin-nick-cancel').addEventListener('click',()=>close('admin-nick-dialog'));$('#admin-nick-dialog').addEventListener('cancel',event=>{if(!nick)event.preventDefault()});
 $('#admin-nick-form').addEventListener('submit',event=>{event.preventDefault();const name=$('#admin-nick-input').value.trim();if(!name||[...name].length>32||/[\u0000-\u001f\u007f]/u.test(name)){$('#admin-nick-error').hidden=false;return}nick=name;try{localStorage.setItem('soundboard.nickname',name)}catch{}$('#admin-nickname').textContent=name;announceAdminIdentity();close('admin-nick-dialog')});
 window.addEventListener('storage',event=>{if(event.key==='soundboard.nickname'&&event.newValue){nick=event.newValue;$('#admin-nickname').textContent=nick;announceAdminIdentity()}});
 if(nick&&[...nick].length<=32){$('#admin-nickname').textContent=nick}else{nick='';openNickDialog()}
 connectAdminPresence();
 load().catch(e=>notice(`Bibliothek konnte nicht geladen werden: ${e.message}`));
})();
