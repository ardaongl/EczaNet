import {test,expect,type Page} from '@playwright/test';
import {createHash,randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
import {allRows,type Sale,type Inventory} from './support/a2';
import {account,db,logout,password,stock,type Account} from './support/fixtures';

const evidence=process.env.A2_EVIDENCE_DIR??'../artifacts/browser-runs/auth';
type Pending={path:string;body:string;key:string};
async function login(page:Page,email:string){
 await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
 await page.getByLabel('E-posta').fill(email);await page.getByLabel('Parola',{exact:true}).fill(password);
 await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.locator('aside .email')).toHaveText(email);
}
async function pending(page:Page,storageKey:string):Promise<Pending|null>{
 return page.evaluate(key=>JSON.parse(sessionStorage.getItem(key)??'null'),storageKey);
}

for(const mode of ['missing','expired','revoked','forbidden'] as const)test(`R-A2-01 ${mode}: belirsiz satış kimliği korunur`,async({page,context})=>{
 test.setTimeout(90000);await page.setViewportSize({width:390,height:1000});
 const prefix='A2-AUTH-'+mode+'-'+randomUUID();const email=prefix.toLowerCase()+'@ddo-case.invalid';
 // Isolate auth faults to a newly created test principal, never the shared seed account.
 const uid=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${email}',password_hash,'pharmacist' FROM "pl-wy-users" WHERE id=1 RETURNING id;`).split('\n')[0];expect(uid).toMatch(/^[0-9]+$/);
 db(`INSERT INTO "pl-user-locations"(user_id,location_id) VALUES(${uid},1);`);
 const warehouse=await account('depo@ddo-case.invalid');const receiver=await account(email);const observer=await account('seed.yildiz@ddo-case.invalid');
 let foreign:Account|undefined;
 try{
  const f=await stock(warehouse,receiver,'10',prefix);const storageKey=`eczanet_pending_${uid}_1_Kasa ve reçete`;
  await page.goto('/');await login(page,email);await page.getByRole('navigation').getByRole('button',{name:'Kasa ve reçete',exact:true}).click();
  await page.getByLabel('Ürün 1',{exact:true}).selectOption(f.product);await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await expect(page.getByRole('button',{name:'Satışı kesinleştir',exact:true})).toBeVisible();
  const sent:Pending[]=[];let dropped=false;
  await page.route('**/api/v1/sales',async route=>{
   if(route.request().method()!=='POST'){await route.continue();return}
   sent.push({path:'/sales',key:route.request().headers()['idempotency-key'],body:route.request().postData()!});
   if(!dropped){dropped=true;const response=await route.fetch();expect(response.status()).toBe(201);await route.abort('failed')}else await route.continue();
  });
  await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).click();await expect(page.getByRole('button',{name:'Sonucu kontrol et',exact:true})).toBeVisible();
  const original=await pending(page,storageKey);expect(original).not.toBeNull();expect(sent).toHaveLength(1);
  const first=(await allRows<Sale>(observer.api,'/api/v1/sales?location_id=1')).filter(s=>s.lines.some(l=>l.product_id===f.product));expect(first).toHaveLength(1);
  if(mode==='missing')await context.clearCookies();
  if(mode==='expired')db(`UPDATE "pl-sessions" SET expires_at=clock_timestamp()-interval '1 second' WHERE user_id=${uid};`);
  if(mode==='revoked')db(`UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id=${uid};`);
  if(mode==='forbidden')db(`UPDATE "pl-user-locations" SET location_id=2 WHERE user_id=${uid} AND location_id=1;`);
  const denied=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/api/v1/sales')&&r.status()===(mode==='forbidden'?403:401));
  await page.getByRole('button',{name:'Sonucu kontrol et',exact:true}).click();const deniedResponse=await denied;
  expect(Object.keys(await deniedResponse.json())).toEqual(['error']);expect(await pending(page,storageKey)).toEqual(original);
  await expect(page.getByRole('button',{name:'Oturumu yenile',exact:true})).toBeVisible();expect(sent).toHaveLength(2);expect(sent[1]).toEqual(sent[0]);
  await expect(page.getByRole('button',{name:'Sunucu teklifini al',exact:true})).toBeDisabled();
  await expect(page.getByRole('button',{name:'Satışı kesinleştir',exact:true})).toBeDisabled();
  await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toHaveCount(0);
  await page.screenshot({path:`${evidence}/auth-${mode}-denied.png`,fullPage:true});
  if(mode==='forbidden'){
   db(`UPDATE "pl-user-locations" SET location_id=1 WHERE user_id=${uid} AND location_id=2;`);
  }else{
   await page.getByRole('button',{name:'Oturumu yenile',exact:true}).click();
   if(mode==='missing'){
    // Another principal must neither see nor automatically replay the unresolved command.
    await login(page,'eczaci.b@ddo-case.invalid');
    await expect(page.getByRole('button',{name:'Sonucu kontrol et',exact:true})).toHaveCount(0);
    await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toHaveCount(0);expect(sent).toHaveLength(2);
    foreign=await account('eczaci.b@ddo-case.invalid');
    const blocked=await foreign.api.post('/api/v1/sales',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':foreign.csrf,'Idempotency-Key':original!.key},data:JSON.parse(original!.body)});
    expect(blocked.status()).toBe(403);expect(Object.keys(await blocked.json())).toEqual(['error']);
    expect(await pending(page,storageKey)).toEqual(original);
    await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();
   }
   await login(page,email);
   if(await page.getByRole('button',{name:'Sonucu kontrol et',exact:true}).count()===0)await page.getByRole('navigation').getByRole('button',{name:'Kasa ve reçete',exact:true}).click();
   expect(await pending(page,storageKey)).toEqual(original);
  }
  await page.getByRole('button',{name:'Sonucu kontrol et',exact:true}).click();await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toBeVisible();
  await expect(page.locator('.receipt')).toContainText('100 kuruş');await expect(page.locator('.receipt')).toContainText(first[0].id);
  expect(sent).toHaveLength(3);expect(sent[2]).toEqual(sent[0]);expect(await pending(page,storageKey)).toBeNull();
  const sales=(await allRows<Sale>(observer.api,'/api/v1/sales?location_id=1')).filter(s=>s.lines.some(l=>l.product_id===f.product));expect(sales).toHaveLength(1);expect(sales[0].id).toBe(first[0].id);expect(sales[0].total).toBe('100');
  const inventory=(await allRows<Inventory>(observer.api,'/api/v1/inventory?location_id=1')).filter(l=>l.product_id===f.product);expect(inventory).toHaveLength(1);expect(inventory[0].balances.physical).toBe('9');expect(inventory[0].balances.sold).toBe('1');
  expect(db(`SELECT count(*) FROM "pl-idempotency" WHERE actor_id=${uid} AND command='sale.create:';`)).toBe('1');
  writeFileSync(`${evidence}/auth-${mode}.json`,JSON.stringify({mode,actor:uid,product:f.product,transfer:f.transfer,sale:sales[0].id,deniedStatus:mode==='forbidden'?403:401,keyAndBodyPreserved:true,requestCount:sent.length,saleCount:sales.length,physical:'9',sold:'1',total:'100',keyFingerprint:createHash('sha256').update(original!.key).digest('hex')},null,2));
  await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();
 }finally{
  // Keep all business records. Restore this principal's assignment and disable only this test account.
  db(`UPDATE "pl-user-locations" SET location_id=1 WHERE user_id=${uid}; UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id=${uid}; UPDATE "pl-wy-users" SET active=false WHERE id=${uid};`);
  if(foreign)await logout(foreign);await logout(receiver);await logout(observer);await logout(warehouse);
 }
});
