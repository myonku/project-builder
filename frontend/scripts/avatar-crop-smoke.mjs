// Node 22+, Chromium CDP and a real test API/Vite. Never run against production data.
// node scripts/avatar-crop-smoke.mjs [9333] [http://127.0.0.1:5173]
// PREVIEW_TEST_USER / PREVIEW_TEST_PASSWORD override the default test bootstrap account.
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
const [port='9333', origin='http://127.0.0.1:5173'] = process.argv.slice(2);
const page = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
if (!page) {
  throw new Error('没有找到 page 类型的调试目标');
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();

let exceptions = 0;

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  } else if (msg.method) {
    if (msg.method === 'Runtime.exceptionThrown') exceptions++;
  }
});

await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true });
  ws.addEventListener('error', reject, { once: true });
});

const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function evaluate(expression) {
  const result = await send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (result.exceptionDetails) {
    throw new Error(`页面内异常: ${JSON.stringify(result.exceptionDetails)}`);
  }
  return result.result.value;
}


async function until(expression) {
 for(let i=0;i<100;i++){if(await evaluate(expression))return;await sleep(100)}
 throw new Error('Browser condition did not become true: '+expression);
}
async function navigate(path, text) {
 await send('Page.navigate',{url:origin+path});
 await until(`(document.body?.innerText ?? '').includes(${JSON.stringify(text)})`);
}

async function click(selector) {
 const point=await evaluate(`(()=>{const e=${selector};e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
 await send('Input.dispatchMouseEvent',{type:'mouseMoved',...point});
 await send('Input.dispatchMouseEvent',{type:'mousePressed',...point,button:'left',clickCount:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',...point,button:'left',clickCount:1});
}
const button = name => `[...document.querySelectorAll('.MuiDialog-paper button')].find(e=>e.textContent.trim()===${JSON.stringify(name)})`;
let uploads=0;
ws.addEventListener('message',event=>{const msg=JSON.parse(event.data);if(msg.method==='Network.requestWillBeSent' && msg.params.request.method==='POST' && msg.params.request.url.endsWith('/me/avatar'))uploads++;});
const output=resolve('.verify/avatar-crop');mkdirSync(output,{recursive:true});
try {
 await send('Runtime.enable');await send('Page.enable');await send('Network.enable');await send('Page.bringToFront');
 await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await navigate('/login','登录');
 const credentials={identifier:process.env.PREVIEW_TEST_USER||'bootstrap_admin',password:process.env.PREVIEW_TEST_PASSWORD||'bootstrap_admin_test_password'};
 assert.ok(await evaluate(`(async()=>{const r=await fetch('/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(${JSON.stringify(credentials)})});return r.ok})()`));
 await navigate('/me','个人信息');
 await until("!!document.querySelector('input[type=file]')");
 const encoded=await evaluate(`(()=>{const c=document.createElement('canvas');c.width=1200;c.height=600;const g=c.getContext('2d');g.fillStyle='red';g.fillRect(0,0,400,600);g.fillStyle='lime';g.fillRect(400,0,400,600);g.fillStyle='blue';g.fillRect(800,0,400,600);return c.toDataURL('image/png').split(',')[1]})()`);
 const file=resolve(output,'source.png');writeFileSync(file,Buffer.from(encoded,'base64'));
 async function pick(){const doc=await send('DOM.getDocument');const node=await send('DOM.querySelector',{nodeId:doc.root.nodeId,selector:'input[type=file]'});await send('DOM.setFileInputFiles',{nodeId:node.nodeId,files:[file]});await until("!!document.querySelector('.reactEasyCrop_CropAreaRound') && document.querySelector('.reactEasyCrop_Image')?.naturalWidth > 0");}
 await pick();assert.equal(uploads,0);await click(button('取消'));await until("!document.querySelector('.MuiDialog-paper')");assert.equal(uploads,0);console.log('PASS cancel does not upload');
 await pick();await evaluate("document.querySelector('input[aria-labelledby=avatar-zoom-label]').focus()");
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'End',code:'End',windowsVirtualKeyCode:35});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'End',code:'End',windowsVirtualKeyCode:35});
 await until("document.querySelector('input[aria-labelledby=avatar-zoom-label]').value==='4'");await sleep(250);
 // Rotation must leave zoom unchanged; reset restores both controls.
 await evaluate("document.querySelector('input[aria-labelledby=avatar-rotation-label]').focus()");
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'End',code:'End',windowsVirtualKeyCode:35});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'End',code:'End',windowsVirtualKeyCode:35});
 assert.equal(await evaluate("document.querySelector('input[aria-labelledby=avatar-zoom-label]').value"),'4');
 await click(button('重置裁剪'));
 assert.equal(await evaluate("document.querySelector('input[aria-labelledby=avatar-zoom-label]').value"),'1');
 assert.equal(await evaluate("document.querySelector('input[aria-labelledby=avatar-rotation-label]').value"),'0');
 await evaluate("document.querySelector('input[aria-labelledby=avatar-rotation-label]').focus()");
 for(let i=0;i<9;i++){await send('Input.dispatchKeyEvent',{type:'keyDown',key:'PageUp',code:'PageUp',windowsVirtualKeyCode:33});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'PageUp',code:'PageUp',windowsVirtualKeyCode:33});}
 await until("document.querySelector('input[aria-labelledby=avatar-rotation-label]').value==='90'");await sleep(250);
 await click(button('保存头像'));await until("!document.querySelector('.MuiDialog-paper')");assert.equal(uploads,1);
 const saved=await evaluate(`(async()=>{const me=(await(await fetch('/api/v1/auth/me')).json()).data;const r=await fetch('/api/v1/users/'+me.id+'/avatar?v='+encodeURIComponent(me.avatar_updated_at));const bytes=new Uint8Array(await r.arrayBuffer());let text='';for(const b of bytes)text+=String.fromCharCode(b);return btoa(text)})()`);
 const pixels=await evaluate(`(async()=>{const image=new Image();image.src='data:image/png;base64,'+${JSON.stringify(saved)};await image.decode();const c=document.createElement('canvas');c.width=c.height=512;const g=c.getContext('2d');g.drawImage(image,0,0);return [32,256,480].map(y=>Array.from(g.getImageData(256,y,1,1).data))})()`);
 assert.deepEqual(pixels,[[255,0,0,255],[0,255,0,255],[0,0,255,255]]);const bytes=Buffer.from(saved,'base64');assert.equal(bytes.readUInt32BE(16),512);assert.equal(bytes.readUInt32BE(20),512);writeFileSync(resolve(output,'saved.png'),bytes);assert.equal(exceptions,0);console.log('PASS independent rotation, reset and rotated 512x512 PNG pixels; zero runtime exceptions');
} finally {ws.close();await fetch(`http://127.0.0.1:${port}/json/close/${page.id}`);}
