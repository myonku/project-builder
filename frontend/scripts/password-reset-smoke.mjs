// W5 real-browser and Mailpit acceptance. No npm dependencies; Node 22+ / Chrome CDP.
// Start API with Mailpit enabled, registration approval disabled (test only), and Vite.
// chromium --headless --no-sandbox --remote-debugging-port=9333 --user-data-dir=/tmp/worklane-w5 about:blank
// node scripts/password-reset-smoke.mjs [9333] [http://127.0.0.1:5173] [http://127.0.0.1:18025]
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync } from 'node:fs';
const [port='9333', origin='http://127.0.0.1:5173', mailpit='http://127.0.0.1:18025'] = process.argv.slice(2);
const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
const page = targets.find((t) => t.type === 'page');
if (!page) {
  throw new Error('没有找到 page 类型的调试目标');
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
let nextId = 1;
const pending = new Map();
const events = [];
let exceptions = 0;

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  } else if (msg.method) {
    events.push(msg.method); if (msg.method === 'Runtime.exceptionThrown') exceptions++;
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
 throw new Error('Browser condition did not become true');
}
async function navigate(path, text) {
 await send('Page.navigate',{url:origin+path});
 await until(`document.body.innerText.includes(${JSON.stringify(text)})`);
}
async function fill(values) {
 await evaluate(`(() => { const values = ${JSON.stringify(values)};
 const inputs = [...document.querySelectorAll('form input')];
 values.forEach((v,i)=>{ Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(inputs[i],v);inputs[i].dispatchEvent(new Event('input',{bubbles:true})); });
 })()`);
}
async function submit(){await evaluate(`document.querySelector('form').requestSubmit()`)}
async function api(path, body, cookie) {
 return fetch(origin+'/api/v1'+path,{method:body?'POST':'GET',headers:{...(body?{'Content-Type':'application/json'}:{}),...(cookie?{Cookie:cookie}:{})},body:body?JSON.stringify(body):undefined});
}
await send('Page.enable'); await send('Runtime.enable');
const username = 'w5_'+Date.now(), email=username+'@example.com', password='original password 123';
assert.equal((await api('/auth/register',{username,email,password,display_name:'密码验收',timezone:'Asia/Shanghai'})).status,201);
const login=await api('/auth/login',{identifier:username,password});assert.equal(login.status,200);
const oldCookie=login.headers.get('set-cookie').split(';')[0];
await navigate('/login','忘记密码');
await evaluate(`[...document.querySelectorAll('button')].find(e=>e.textContent.includes('忘记密码')).click()`);
await until(`location.pathname==='/forgot-password' && !!document.querySelector('input[type=email]')`);
await fill([email]);await submit();await until(`document.body.innerText.includes('如果该邮箱符合找回条件')`);
let token='';
for(let i=0;i<50&&!token;i++){
 const listing=await (await fetch(mailpit+'/api/v1/search?query='+encodeURIComponent('to:'+email))).json();
 for(const m of listing.messages??[]){const mail=await(await fetch(mailpit+'/api/v1/message/'+m.ID)).json();token=mail.Text?.match(/token=([0-9a-f]{64})/)?.[1]??'';if(token)break}
 if(!token)await sleep(100);
}
assert.match(token,/^[0-9a-f]{64}$/);
await navigate('/reset-password?token='+token,'设置新密码');
await until(`location.search===''`);
assert.equal(await evaluate(`document.referrer.includes('token=')`),false);
await fill(['new password 1234','mismatched password']);await submit();await until(`document.body.innerText.includes('两次输入的密码不一致')`);
mkdirSync('.verify/password-reset',{recursive:true});
const screenshot=async(name)=>{const shot=await send('Page.captureScreenshot',{format:'png'});writeFileSync('.verify/password-reset/'+name+'.png',Buffer.from(shot.data,'base64'))};
await screenshot('reset-wide');
await send('Emulation.setDeviceMetricsOverride',{width:480,height:900,deviceScaleFactor:1,mobile:true});
await until(`document.documentElement.scrollWidth<=innerWidth`);await screenshot('reset-narrow');
await fill(['new password 1234','new password 1234']);await submit();await until(`document.body.innerText.includes('密码已重置')`);
assert.equal((await api('/auth/me',undefined,oldCookie)).status,401);
assert.equal((await api('/auth/login',{identifier:username,password})).status,401);
assert.equal((await api('/auth/login',{identifier:username,password:'new password 1234'})).status,200);
await navigate('/reset-password?token='+token,'设置新密码');await fill(['another new password','another new password']);await submit();await until(`document.body.innerText.includes('重置链接已失效')`);
await navigate('/reset-password','设置新密码');await until(`document.body.innerText.includes('重置链接无效')`);
assert.equal(await evaluate(`document.querySelector('button[type=submit]').disabled`),true);
assert.equal(exceptions,0);
console.log('W5 browser + SMTP acceptance passed: email, mismatch, reset, replay, invalid link, old session 401, new login, wide/narrow, URL cleanup, zero runtime exceptions.');
ws.close();
