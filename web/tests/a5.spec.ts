import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
import {account,logout,post,db,password} from './support/fixtures';
const evidence=process.env.A5_EVIDENCE_DIR;
if(!evidence)throw new Error('A5_EVIDENCE_DIR is required');
for(const width of [390,1280])test(`A5 açıklama ihtiyaç iade ve rapor ${width}`,async({page})=>{
 test.setTimeout(180000);await page.setViewportSize({width,height:1000});const tag='A5-UI-'+randomUUID();
 const branch=db(`INSERT INTO "pl-locations"(name,kind) VALUES('${tag}','branch') RETURNING id;`).split('\n')[0];
 const actor=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${tag.toLowerCase()}@invalid',password_hash,'manager' FROM "pl-wy-users" WHERE id=4 RETURNING id;`).split('\n')[0];db(`INSERT INTO "pl-user-locations" VALUES(${actor},${branch});`);
 const w=await account('depo@ddo-case.invalid');
 try{
  const p=await post(w,'/products',{location_id:'21',product:{barcode:'00'+Date.now()+width,name:tag,dose:'5mg',form:'tablet',base_unit:'tablet',pack_size:'1',blister_size:'0',sale_multiple:'1',ingredients:['a5-ui'],pack_price:'100'}});
  db(`INSERT INTO "pl-suggestion-policy"(product_id,location_id,version,minimum) VALUES(${p.id},${branch},'ui-v1',20);`);
  const expiry=new Date(Date.now()+86400000).toISOString().slice(0,10);
  await post(w,'/goods-receipts',{location_id:'21',supplier_id:'1',document_no:tag,reason:'A5 UI near expiry',lines:[{product_id:p.id,lot:tag,expires_on:expiry,quantity:'10'}]});
  // Transfer is delivered by a temporary assigned pharmacist via the established API.
  const pharmacist=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${tag.toLowerCase()}-p@invalid',password_hash,'pharmacist' FROM "pl-wy-users" WHERE id=1 RETURNING id;`).split('\n')[0];db(`INSERT INTO "pl-user-locations" VALUES(${pharmacist},${branch});`);
  const a=await account(tag.toLowerCase()+'-p@invalid');const tr=await post(w,'/transfers',{location_id:'21',target_id:branch,items:[{product_id:p.id,quantity:'10',unit:'unit'}]});await post(w,'/transfers/'+tr.id+'/send',{location_id:'21'},200);await post(a,'/transfers/'+tr.id+'/receive',{location_id:branch},200);await logout(a);db(`UPDATE "pl-wy-users" SET active=false WHERE id=${pharmacist};`);
  await page.goto('/');await page.getByLabel('E-posta').fill(tag.toLowerCase()+'@invalid');await page.getByLabel('Parola',{exact:true}).fill(password);await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.getByRole('heading',{name:'Çalışma alanı',exact:true})).toBeVisible();
  await page.getByRole('navigation').getByRole('button',{name:'Öneriler',exact:true}).click();
  const calc=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/v1/suggestion-runs');await page.getByRole('button',{name:'Önerileri yeniden hesapla',exact:true}).click();const runResponse=await calc;expect(runResponse.status(),await runResponse.text()).toBe(201);const run=await runResponse.json();
  const article=page.locator('article').filter({has:page.getByRole('heading',{name:tag,exact:true})});
  for(let i=0;await article.count()===0&&i<50;i++){await expect(page.getByRole('status').filter({hasText:'Veriler yükleniyor'})).toHaveCount(0);if(await article.count())break;const response=page.waitForResponse(r=>r.request().method()==='GET'&&new URL(r.url()).pathname==='/api/v1/suggestions'&&!!new URL(r.url()).searchParams.get('cursor'));await page.getByRole('button',{name:'Sonraki sayfa',exact:true}).click();await response;}
  await expect(article).toHaveCount(1);await expect(article.getByText('İade inceleme miktarı',{exact:true})).toBeVisible();await article.getByLabel('Öneri gerekçesi').fill('Müdür incelemesi');
  // Transient failure retains the entered form and the same idempotent operation.
  await page.route('**/api/v1/suggestions/*/need',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:{code:'UNAVAILABLE',message:'A5 test kesintisi'}})}),{times:1});
  await article.getByRole('button',{name:'İhtiyaç taslağı oluştur',exact:true}).click();await expect(page.getByRole('alert')).toContainText('A5 test kesintisi');await expect(article.getByLabel('Öneri gerekçesi')).toHaveValue('Müdür incelemesi');await page.getByRole('button',{name:'Sonucu kontrol et',exact:true}).click();await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toBeVisible();
  // Refresh returns the first page; locate the exact product again through all pages.
  for(let i=0;await article.count()===0&&i<50;i++){await expect(page.getByRole('status').filter({hasText:'Veriler yükleniyor'})).toHaveCount(0);if(await article.count())break;const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/v1/suggestions'&&!!new URL(r.url()).searchParams.get('cursor'));await page.getByRole('button',{name:'Sonraki sayfa',exact:true}).click();await response;}
  await article.getByLabel('Öneri gerekçesi').fill('Satışsız SKT incelemesi');const draftResponse=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname.endsWith('/return'));await article.getByRole('button',{name:'İade inceleme transferi oluştur',exact:true}).click();expect((await draftResponse).status()).toBe(201);
  const physical=db(`SELECT sum(b.quantity) FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id WHERE l.product_id=${p.id} AND b.location_id=${branch} AND account='physical';`);expect(physical).toBe('10');
  await page.getByRole('navigation').getByRole('button',{name:'Raporlar',exact:true}).click();await expect(page.getByRole('heading',{name:tag,exact:true})).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);await page.screenshot({path:`${evidence}/a5-${width}.png`,fullPage:true});
  writeFileSync(`${evidence}/a5-${width}.json`,JSON.stringify({product:p.id,branch,actor,run,physical},null,2));
 }finally{db(`UPDATE "pl-wy-users" SET active=false WHERE id=${actor}; UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id=${actor};`);await logout(w)}
});
