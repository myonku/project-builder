// Node 22+, Chromium CDP and a real test API/Vite. Never run against production data.
// node scripts/rollup-smoke.mjs [9333] [http://127.0.0.1:5173]
// PREVIEW_TEST_USER / PREVIEW_TEST_PASSWORD override the default test bootstrap account.
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {execFileSync} from 'node:child_process';
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(process.argv[3]||'http://127.0.0.1:5178')) throw new Error('P02 browser tests require isolated loopback origin');
const [port='9338', origin='http://127.0.0.1:5178'] = process.argv.slice(2);
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

const output=resolve('.verify/p02');mkdirSync(output,{recursive:true});
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

// Uses only the dedicated worklane-p02 Compose fixture database. All business
// creation and reads use the real API. SQL seeds exceptional lifecycle evidence
// so every new UI state can be exercised without contacting a provider.
try {
 await send('Runtime.enable');await send('Page.enable');
 await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
 await navigate('/login','登录');
 await api('/auth/login','POST',{identifier:process.env.PREVIEW_TEST_USER||'bootstrap_admin',password:process.env.PREVIEW_TEST_PASSWORD||'bootstrap_admin_test_password'});
 const group=await api('/groups','POST',{name:'P02 browser '+Date.now()});
 const project=await api('/projects','POST',{name:'P02 browser',group_id:group.id,start_date:'2026-09-01',end_date:'2026-12-31'});
 const makeTask=async(name)=>api('/projects/'+project.id+'/tasks','POST',{name,start_date:'2026-10-01',end_date:'2026-10-28'});
 const pendingTask=await makeTask('待发与改期验证');
 const unknownTask=await makeTask('未知结果验证');
 const suppressedTask=await makeTask('同日去重验证');
 const sendingTask=await makeTask('发送中验证');const cancelledTask=await makeTask('取消验证');const sentTask=await makeTask('成功历史验证');const failedTask=await makeTask('安全失败重试验证');
 const remindersFor=async(task)=>api('/reminders?task_id='+task.id+'&page_size=100');
 const getOverdue=async(task)=>(await remindersFor(task)).find(r=>r.kind==='task_overdue');
 const initial=await getOverdue(pendingTask);
 assert.equal(initial.status,'pending');assert.equal(initial.can_retry,false);
 const changed=await api('/tasks/'+pendingTask.id,'PATCH',{end_date:'2026-11-06',expected_updated_at:pendingTask.updated_at});
 const rescheduled=await getOverdue(changed);
 assert.equal(rescheduled.status,'pending');assert.equal(rescheduled.cycle,initial.cycle+1);
 assert.notEqual(rescheduled.id,initial.id);
 assert.equal((await api('/reminders/'+initial.id)).status,'superseded');
 const unknown=await getOverdue(unknownTask);const suppressed=await getOverdue(suppressedTask);const sending=await getOverdue(sendingTask);const sent=await getOverdue(sentTask);const failed=await getOverdue(failedTask);
 await api('/tasks/'+cancelledTask.id,'PATCH',{status:'cancelled',expected_updated_at:cancelledTask.updated_at});const cancelled=await getOverdue(cancelledTask);
 for (const id of [unknown.id,suppressed.id]) assert.match(id,/^[0-9a-f-]{36}$/);
 const seed=(id,status)=>{
  assert.match(id,/^[0-9a-f-]{36}$/);const attemptStatus=status==='suppressed'?'sent':status;
  assert.ok(['sent','sending','unknown','failed'].includes(attemptStatus));
  const sql=`WITH a AS (INSERT INTO reminder_delivery_attempts(reminder_id,task_id,recipient_id,kind,plan_version,cycle,authorized_at,expires_at,status,sent_at,timezone,snapshot,error)
    SELECT id,task_id,recipient_id,kind,plan_version,cycle,now(),now()+interval '2 minutes','${attemptStatus}',CASE WHEN '${attemptStatus}'='sent' THEN now() ELSE NULL END,'UTC',
      jsonb_build_object('To',recipient_email,'Subject','P02 原始邮件快照','Body',repeat('原始正文 ',40)),'本地页面验收夹具'
    FROM reminders WHERE id='${id}' RETURNING *), g AS (UPDATE reminder_delivery_guards g SET active_attempt=CASE WHEN a.status IN ('unknown','sending') THEN a.id ELSE NULL END FROM a WHERE (g.task_id,g.recipient_id,g.kind)=(a.task_id,a.recipient_id,a.kind))
    UPDATE reminders r SET status='${status}',sent_at=CASE WHEN '${status}'='sent' THEN now() ELSE NULL END,
      covered_by=CASE WHEN '${status}'='suppressed' THEN a.id ELSE NULL END,attempts=r.attempts+1,last_error='本地页面验收夹具' FROM a WHERE r.id=a.reminder_id;`;
  execFileSync('docker',['exec','worklane-p02-postgres-1','psql','-v','ON_ERROR_STOP=1','-U','project_builder_test','-d','worklane_p02_browser','-c',sql]);
 };
 seed(unknown.id,'unknown');seed(suppressed.id,'suppressed');seed(sending.id,'sending');seed(sent.id,'sent');seed(failed.id,'failed');
 assert.equal((await api('/reminders/'+unknown.id)).status,'unknown');
 const cases=[{id:rescheduled.id,label:'待发送'},{id:unknown.id,label:'结果待核实'},{id:suppressed.id,label:'同日已提醒'},{id:sending.id,label:'发送中'},{id:sent.id,label:'已发送'},{id:cancelled.id,label:'已取消'},{id:initial.id,label:'已替代'},{id:failed.id,label:'发送失败',retry:true}];
 const results=[];
 for (const [width,height] of [[1440,1000],[390,844]]) {
  await send('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:width<500});
  for (const test of cases) {
   if(test.retry)seed(test.id,'failed');
   await navigate('/reminders?reminderId='+test.id,'提醒详情');
   await until(`[...document.querySelectorAll('.MuiDrawer-paper')].at(-1)?.innerText.includes(${JSON.stringify(test.label)})`);
   assert.equal(await evaluate(`[...document.querySelectorAll('.MuiDrawer-paper button')].find(b=>b.textContent.trim()==='重试')?.disabled`),!test.retry);
   if(test.id===unknown.id) {await until("[...document.querySelectorAll('.MuiDrawer-paper')].at(-1).innerText.includes('P02 原始邮件快照')");assert.equal(await evaluate("[...document.querySelectorAll('.MuiDrawer-paper')].at(-1).innerText.includes('请联系管理员核实')"),true);}
   assert.equal(await evaluate('document.documentElement.scrollWidth>innerWidth'),false);
   const bounds=await evaluate("(()=>{const r=[...document.querySelectorAll('.MuiDrawer-paper')].at(-1).getBoundingClientRect();return {top:r.top,bottom:r.bottom,width:r.width}})()");
   assert.ok(bounds.top>= (width<500?56:64));assert.ok(bounds.bottom<=height+1);
   await screenshot(width+'-'+test.label);
   if(test.retry){await click("[...document.querySelectorAll('.MuiDrawer-paper button')].find(b=>b.textContent.trim()==='重试')");await until("document.querySelector('.MuiDialog-paper')?.innerText.includes('确认重试')");await click(button('确认重试'));await until("!document.querySelector('.MuiDialog-paper')");await until("[...document.querySelectorAll('.MuiDrawer-paper')].at(-1)?.innerText.includes('待发送')");assert.equal((await api('/reminders/'+test.id)).status,'pending');}
   await click("document.querySelector('[aria-label=\"关闭提醒详情\"]')");
   await until("!location.search.includes('reminderId')");
   results.push({width,state:test.label,retryEnabled:!!test.retry,layout:true});
  }
 }
 assert.equal(exceptions,0);writeFileSync(resolve(output,'result.json'),JSON.stringify({results,exceptions,project:project.id},null,2));
 console.log(JSON.stringify({passed:results.length,exceptions}));
} finally {ws.close();}
