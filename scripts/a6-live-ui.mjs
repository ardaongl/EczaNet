import {chromium,expect} from '../web/node_modules/@playwright/test/index.mjs';
import {execFileSync} from 'node:child_process';
import {existsSync,mkdirSync,writeFileSync} from 'node:fs';
import {resolve} from 'node:path';
const outputIndex=process.argv.indexOf('--out');const out=resolve(outputIndex>=0?process.argv[outputIndex+1]:'artifacts/a6-live-ui');if(existsSync(out))throw new Error('new evidence directory required');mkdirSync(out,{recursive:true});
const browser=await chromium.launch({headless:true});
try{
 const page=await browser.newPage({viewport:{width:1280,height:900}});await page.goto('http://localhost:13006');await page.getByLabel('E-posta',{exact:true}).fill('seed.yildiz@ddo-case.invalid');await page.getByLabel('Parola',{exact:true}).fill(process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!');await page.getByRole('button',{name:'Giriş yap',exact:true}).click();const region=page.getByRole('region',{name:'Canlı özet'});await expect(region.getByRole('heading',{name:'Canlı çalışma özeti'})).toBeVisible();
 const before=await(await page.request.get('http://localhost:13006/api/v1/dashboard')).json();
 execFileSync('docker',['compose','-p','eczanet-a6','-f','compose.yaml','-f','tests/compose.a6.yaml','restart','simulator'],{stdio:'pipe'});
 let after;
 await expect.poll(async()=>{after=await(await page.request.get('http://localhost:13006/api/v1/dashboard')).json();return after.simulator?.progress?.next??0},{timeout:10000}).toBeGreaterThan(before.simulator.progress.next);
 const last=after.simulator.progress.last_event;await expect(region).toContainText('satış '+last.sale_id,{timeout:10000});const uiLatency=Date.now()-Date.parse(last.completed_at);expect(uiLatency).toBeLessThan(10000);expect(BigInt(after.sale_count)).toBeGreaterThan(BigInt(before.sale_count));expect(BigInt(after.physical)).toBeLessThan(BigInt(before.physical));
 await page.screenshot({path:resolve(out,'dashboard-live-1280.png'),fullPage:true});await page.setViewportSize({width:390,height:900});await page.screenshot({path:resolve(out,'dashboard-live-390.png'),fullPage:true});
 writeFileSync(resolve(out,'result.json'),JSON.stringify({ui_latency_ms:uiLatency,before,after,source:'actual simulator restart -> actual sale API -> dashboard polling',viewport:[390,1280]},null,2));console.log('PASS real simulator API -> UI latency',uiLatency,'ms');
}finally{await browser.close()}
