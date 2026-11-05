import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {existsSync,readFileSync,writeFileSync,mkdirSync,mkdtempSync,rmSync} from 'node:fs';
import {resolve,join,relative} from 'node:path';
import {tmpdir} from 'node:os';
const origin=process.argv[2];assert.match(origin,/^http:\/\/127\.0\.0\.1:\d+$/);
const out=resolve('.verify/p04-v2');mkdirSync(out,{recursive:true});
const profile = mkdtempSync(join(tmpdir(), 'worklane-p04-'));
const executable = process.env.CHROME_PATH || 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const chrome = spawn(executable, ['--headless=new', '--no-first-run', '--disable-gpu', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { windowsHide: true, stdio: 'ignore' });
let ws; let checks = 0; const errors = [];
const pause = ms => new Promise(r => setTimeout(r, ms));
const check = (value, label) => { assert.ok(value, label); checks++; };
try {
  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; !existsSync(portFile) && i < 100; i++) await pause(100);
  const port = readFileSync(portFile, 'utf8').split('\n')[0];
  const target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(t => t.type === 'page');
  ws = new WebSocket(target.webSocketDebuggerUrl);
  const pending = new Map(); let nextId = 1;
  ws.onmessage = event => {
    const msg = JSON.parse(event.data);
    if (msg.id) { const p = pending.get(msg.id); pending.delete(msg.id); if (msg.error) p.reject(msg.error); else p.resolve(msg.result); }
    if (msg.method === 'Runtime.exceptionThrown') errors.push(msg.params.exceptionDetails.text);
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') errors.push(JSON.stringify(msg.params.args));
  };
  await new Promise((r, reject) => { ws.onopen = r; ws.onerror = reject; });
  const send = (method, params = {}) => new Promise((resolve, reject) => { const id = nextId++; pending.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params })); });
  const evaluate = async expression => {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    return result.result.value;
  };
  const wait = async expression => { for (let i = 0; i < 150; i++) { if (await evaluate(expression)) return; await pause(50); } throw new Error('Timed out: ' + expression); };
  const body = () => evaluate('document.body.innerText');
  const click = async text => { check(await evaluate(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.includes(${JSON.stringify(text)}) && !b.disabled); if(!b)return false; b.click(); return true})()`), 'button ' + text); await pause(100); };
  const go = async path => { await send('Page.navigate', { url: origin + path }); await wait('!!document.querySelector("main")'); };
  const navigate = async path => { await evaluate(`history.pushState({}, '', ${JSON.stringify(path)}); dispatchEvent(new PopStateEvent('popstate'))`); await pause(100); };
  const ready = () => wait('document.body.innerText.includes("以上为组级范围")');
  const screenshot = async name => { const r = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); writeFileSync(join(out, name + '.png'), Buffer.from(r.data, 'base64')); };
  await send('Runtime.enable'); await send('Page.enable');

  await send('Network.enable');
  await send('Network.setCookie', {name:process.env.INSIGHTS_COOKIE_NAME,value:process.env.INSIGHTS_COOKIE,url:origin});
  await send('Emulation.setDeviceMetricsOverride',{width:1920,height:1080,deviceScaleFactor:1,mobile:false});
  await go('/'); await wait('document.body.innerText.includes("未来14天到期安排")');
  check((await body()).includes('本人承担'),'dashboard scope'); await screenshot('dashboard-v2');
  const reportPath='/statistics?page=report&groupId='+process.env.INSIGHTS_GROUP;
  await go(reportPath); await wait('document.querySelectorAll(".report-sheet").length===4');
  check((await body()).includes('周期汇报'),'report workspace');
  check(!(await evaluate('[...document.querySelectorAll("input")].some(e=>e.type==="date")')),'MUI date controls');
  const layoutFits = '(()=>{const e=[...document.querySelectorAll(".report-sheet")].find(e=>getComputedStyle(e).display!=="none");const title=e.querySelector(".report-title");const content=e.querySelector(".report-content");const footer=e.querySelector(".report-footer");const tr=title.getBoundingClientRect();const children=[...content.children].map(x=>x.getBoundingClientRect());return e.scrollHeight<=e.clientHeight+2 && tr.height>=parseFloat(getComputedStyle(title).lineHeight)-1 && children[0].top>=tr.bottom-1 && children.every((r,i)=>!i || r.top>=children[i-1].bottom-1) && children.at(-1).bottom<=footer.getBoundingClientRect().top+1})()';
  await click('开始演示'); await wait('!!document.querySelector(".report-presentation")');
  for(let i=0;i<4;i++) {
   if(i) await click('下一屏');
   check(await evaluate(layoutFits),'presentation screen fits '+i);
   await screenshot('presentation-'+i);
  }
  await send('Emulation.setDeviceMetricsOverride',{width:1366,height:768,deviceScaleFactor:1,mobile:false});
  for(let i=0;i<3;i++)await click('上一屏');
  for(let i=0;i<4;i++){if(i)await click('下一屏');check(await evaluate(layoutFits),'1366 presentation fits '+i);}
  for (const [width,height] of [[1710,920],[1366,650]]) {
   await send('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false});
   for(let i=0;i<3;i++)await click('上一屏');
   for(let i=0;i<4;i++){if(i)await click('下一屏');check(await evaluate(layoutFits),`${width}x${height} title/content/footer do not overlap, screen ${i}`);}
  }
  await send('Input.dispatchKeyEvent',{type:'keyDown',key:'Escape',windowsVirtualKeyCode:27});
  await wait('!document.querySelector(".report-presentation")');checks++;
  await send('Emulation.setEmulatedMedia',{media:'print'});
  check(await evaluate('[...document.querySelectorAll(".report-sheet")].every(e=>getComputedStyle(e).display!=="none")'),'all print sheets');
  const pdf=await send('Page.printToPDF',{printBackground:true,preferCSSPageSize:true});writeFileSync(join(out,'report-v2.pdf'),Buffer.from(pdf.data,'base64'));
  await send('Emulation.setEmulatedMedia',{media:''});
  await send('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true});
  await go('/statistics?scope=self');await wait('document.body.innerText.includes("未来14天到期安排")');
  check(await evaluate('document.documentElement.scrollWidth<=innerWidth'),'mobile width');await screenshot('overview-mobile-v2');
  check(errors.length===0,'no runtime errors: '+errors.join(','));
  writeFileSync(join(out,'browser-v2.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));
} finally {ws?.close();chrome.kill();await pause(500);if(!relative(tmpdir(),profile).startsWith('..')){try{rmSync(profile,{recursive:true,force:true})}catch{}}}
