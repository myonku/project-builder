// Node 22+, Chromium CDP and a real test API/Vite. Never run against production data.
// node scripts/rollup-smoke.mjs [9333] [http://127.0.0.1:5173]
// PREVIEW_TEST_USER / PREVIEW_TEST_PASSWORD override the default test bootstrap account.
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
const [port='9335', origin='http://127.0.0.1:5175'] = process.argv.slice(2);
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
 for(let i=0;i<100;i++){if(await evaluate(`Boolean(${expression})`))return;await sleep(100)}
 throw new Error('Browser condition did not become true: '+expression);
}
async function navigate(path, text) {
 await send('Page.navigate',{url:origin+path});
 await until(`(document.body?.innerText ?? '').includes(${JSON.stringify(text)})`);
}

async function click(selector) {
 await sleep(350);
 const point=await evaluate(`(()=>{const e=${selector};e.scrollIntoView({block:'center'});const r=e.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
 await send('Input.dispatchMouseEvent',{type:'mouseMoved',...point});
 await send('Input.dispatchMouseEvent',{type:'mousePressed',...point,button:'left',clickCount:1});
 await send('Input.dispatchMouseEvent',{type:'mouseReleased',...point,button:'left',clickCount:1});
}
const button = name => `[...document.querySelectorAll('.MuiDialog-paper button')].find(e=>e.textContent.trim()===${JSON.stringify(name)})`;

const output=resolve('.verify/rollup');mkdirSync(output,{recursive:true});
async function api(path, method='GET', body) {
 const response=await evaluate(`(async()=>{const r=await fetch('/api/v1'+${JSON.stringify(path)}, {method:${JSON.stringify(method)},headers:{'Content-Type':'application/json'},body:${body===undefined?'undefined':JSON.stringify(JSON.stringify(body))}});return {status:r.status,body:await r.json()}})()`);
 assert.ok(response.status<300,JSON.stringify(response)); return response.body.data;
}
async function input(selector,value) {
 await click(selector);
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});
 await send('Input.dispatchKeyEvent',{type:'keyUp',key:'a',code:'KeyA',windowsVirtualKeyCode:65,modifiers:2});
 await send('Input.insertText',{text:String(value)});
}
async function screenshot(name) { await sleep(350); const shot=await send('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});writeFileSync(resolve(output,name+'.png'),Buffer.from(shot.data,'base64')); }
const editButton = "[...document.querySelectorAll('button')].filter(e=>e.textContent.trim()==='编辑').at(-1)";
try {
 await send('Runtime.enable');await send('Page.enable');
 await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await navigate('/login','登录');
 await api('/auth/login','POST',{identifier:process.env.PREVIEW_TEST_USER||'bootstrap_admin',password:process.env.PREVIEW_TEST_PASSWORD||'bootstrap_admin_test_password'});
 const stamp=Date.now();const group=await api('/groups','POST',{name:'汇总验收'+stamp});
 const projectBody={name:'层级汇总'+stamp,group_id:group.id,start_date:'2026-10-01',end_date:'2026-10-31'};
 const project=await api('/projects','POST',projectBody);
 const empty=await api('/projects','POST',{...projectBody,name:'手工进度'+stamp});
 async function task(name,parent_id=null){return api('/projects/'+project.id+'/tasks','POST',{name,parent_id,start_date:'2026-10-01',end_date:'2026-10-20'});}
 const parent=await task('汇总父任务');const root=await task('独立一级任务');const leaf=await task('待完成叶子',parent.id);const completed=await task('完成叶子',parent.id);
 for(const item of [root,completed]){await api('/tasks/'+item.id,'PATCH',{progress:100,expected_updated_at:item.updated_at});}
 assert.equal((await api('/projects/'+project.id)).progress,75);
 await navigate('/projects/'+empty.id,empty.name);await click(editButton);
 await until("document.querySelector('.MuiDialog-paper input[type=number]')");
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper input[type=number]').disabled"),false);
 await input("document.querySelector('.MuiDialog-paper input[type=number]')",70);
 await click(button('保存'));await until("!document.querySelector('.MuiDialog-paper')");
 assert.equal((await api('/projects/'+empty.id)).progress,70);
 console.log('PASS empty project manual progress saves through UI');
 await navigate('/projects/'+project.id,project.name);await click(editButton);
 await until("document.querySelector('.MuiDialog-paper input[type=number]')");
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper input[type=number]').disabled"),true);
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper [role=combobox]').getAttribute('aria-disabled') === 'true'"),true);
 await screenshot('project-rollup-readonly');await click(button('取消'));
 await navigate('/projects/'+project.id+'?taskId='+parent.id,'汇总父任务');
 await until("[...document.querySelectorAll('button')].filter(e=>e.textContent.trim()==='编辑').length===2");await click(editButton);
 await until("document.querySelector('.MuiDialog-paper input[type=range]')");
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper input[type=range]').disabled"),true);
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper [role=combobox]').getAttribute('aria-disabled') === 'true'"),true);
 await screenshot('parent-rollup-readonly');await click(button('取消'));
 await navigate('/projects/'+project.id+'?taskId='+leaf.id,'待完成叶子');
 await until("[...document.querySelectorAll('button')].filter(e=>e.textContent.trim()==='编辑').length===2");await click(editButton);
 await until("document.querySelector('.MuiDialog-paper input[type=range]')");
 assert.equal(await evaluate("document.querySelector('.MuiDialog-paper input[type=range]').disabled"),false);
 await evaluate("document.querySelector('.MuiDialog-paper input[type=range]').focus()");
 await send('Input.dispatchKeyEvent',{type:'keyDown',key:'End',code:'End',windowsVirtualKeyCode:35});await send('Input.dispatchKeyEvent',{type:'keyUp',key:'End',code:'End',windowsVirtualKeyCode:35});
 await click(button('保存'));await until("!document.querySelector('.MuiDialog-paper')");
 assert.equal((await api('/projects/'+project.id)).progress,100);
 await until("document.body.innerText.includes('100%') && document.body.innerText.includes('已完成')");
 console.log('PASS parent/project readonly; leaf edit propagates to ancestors and project');
 await navigate('/projects/'+project.id,project.name);
 await click("[...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='归档')");
 await until("[...document.querySelectorAll('button')].some(e=>e.textContent.trim()==='确认归档')");await click("[...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='确认归档')");
 await until("[...document.querySelectorAll('button')].some(e=>e.textContent.trim()==='解除归档') && !document.querySelector('.MuiDialog-paper')");
 assert.equal((await api('/projects/'+project.id)).status,'archived');
 await click("[...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='解除归档')");
 await until("document.querySelectorAll('.MuiDialog-paper button').length>0");await click(button('解除归档'));
 await until("!document.querySelector('.MuiDialog-paper') && ![...document.querySelectorAll('button')].some(e=>e.textContent.trim()==='解除归档')");
 assert.equal((await api('/projects/'+project.id)).status,'completed');
 console.log('PASS separate archive/unarchive restores derived status');
 await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true});
 await navigate('/projects/'+project.id+'?taskId='+parent.id,'汇总父任务');
 await until("[...document.querySelectorAll('button')].filter(e=>e.textContent.trim()==='编辑').length===2");await click(editButton);
 await until("document.querySelector('.MuiDialog-paper input[type=range]')");
 assert.equal(await evaluate('document.documentElement.scrollWidth<=innerWidth'),true);
 assert.equal(await evaluate("(()=>{const r=document.querySelector('.MuiDialog-paper').getBoundingClientRect();return r.left>=0&&r.right<=innerWidth})()"),true);
 await screenshot('mobile-parent-rollup');assert.equal(exceptions,0);
 writeFileSync(resolve(output,'result.json'),JSON.stringify({project:project.id,empty:empty.id,exceptions,passed:true},null,2));
 console.log('PASS 390px layout; zero runtime exceptions');
} finally { ws.close(); }
