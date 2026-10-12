'use strict';
let chatBusy = false;
document.addEventListener('submit', async event => {
 const form = event.target;
 if (!form.matches('.chat-form')) return;
 event.preventDefault();
 if (chatBusy) return;
 const data = new FormData(form);
 if (event.submitter && event.submitter.name) data.set(event.submitter.name, event.submitter.value);
 chatBusy = true;
 const status = document.getElementById('chat-status');
 status.textContent = 'Виконую запит…';
 document.querySelectorAll('.chat-form button').forEach(b => b.disabled = true);
 try {
  const response = await fetch('/chat', {method:'POST', headers:{'X-Chat-Request':'1'}, body:new URLSearchParams(data)});
  if (!response.ok) throw new Error(await response.text());
  const result = await response.json();
  document.getElementById('workspace').innerHTML = result.html;
  document.querySelector('#composer textarea').focus();
  document.getElementById('composer').scrollIntoView({block:'end',behavior:'smooth'});
 } catch(error) {
  status.textContent = error.message + '\nЯкщо зв’язок перервався, повторіть те саме повідомлення: завершена дія вдруге не виконається. При конфлікті вкладок оновіть сторінку.';
 } finally {
  chatBusy = false;
  document.querySelectorAll('.chat-form button').forEach(b => b.disabled = false);
 }
});
document.addEventListener('keydown',event=>{if(event.target.matches('#composer textarea') && event.key==='Enter' && !event.shiftKey && !event.isComposing){event.preventDefault();event.target.form.requestSubmit();}});
document.addEventListener('input',event=>{if(event.target.id==='tag-filter'){const q=event.target.value.toLocaleLowerCase();document.querySelectorAll('.tag-panel .chat-form').forEach(f=>f.hidden=!f.textContent.toLocaleLowerCase().includes(q));}});
