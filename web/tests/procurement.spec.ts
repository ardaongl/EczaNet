import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {account,logout,post,db,password,type Account} from './support/fixtures';
import {allRows,findOnPages} from './support/a2';
const evidence=process.env.A4_EVIDENCE_DIR??fileURLToPath(new URL('../../artifacts/browser-runs/procurement/',import.meta.url));
for(const width of [390,1280])test(`A4 sipariş tahsis ve uygun uygunsuz kabul ${width}`,async({page})=>{
 test.setTimeout(120000);page.setDefaultTimeout(20000);await page.setViewportSize({width,height:1000});const tag='a4-proc-'+randomUUID();const branch=db(`INSERT INTO "pl-locations"(name,kind) VALUES('${tag}','branch') RETURNING id;`).split('\n')[0];
 const actors:string[]=[];let warehouse:Account|undefined;const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 function actor(role:string){const email=tag+'-'+role+'@ddo-case.invalid';const id=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${email}',password_hash,'${role}' FROM "pl-wy-users" WHERE id=1 RETURNING id;`).split('\n')[0];actors.push(id);db(`INSERT INTO "pl-user-locations" VALUES(${id},${branch});`);return email}
 const manager=actor('manager'),pharmacist=actor('pharmacist');
 async function login(email:string){await page.goto('/');await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();await page.getByLabel('E-posta').fill(email);await page.getByLabel('Parola',{exact:true}).fill(password);await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.getByRole('heading',{name:'Çalışma alanı',exact:true})).toBeVisible()}
 async function menu(name:string){await page.getByRole('navigation').getByRole('button',{name,exact:true}).click();await expect(page.getByRole('status').filter({hasText:'Veriler yükleniyor'})).toHaveCount(0)}
 async function action(path:string,click:()=>Promise<unknown>,status=201){const wait=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/v1'+path);await click();const response=await wait;expect(response.status(),await response.text()).toBe(status);return response.json()}
 try{
  warehouse=await account('depo@ddo-case.invalid');const barcode='00'+Date.now()+width;
  const p=await post(warehouse,'/products',{location_id:'21',product:{barcode,name:tag,dose:'5mg',form:'tablet',base_unit:'tablet',pack_size:'10',blister_size:'0',sale_multiple:'1',ingredients:['a4-proc-ui'],prescription_required:false,controlled_group:'',pack_price:'1000'}});
  await login(manager);await menu('İhtiyaçlar');await page.getByLabel('İhtiyaç ürünü').selectOption(p.id);await page.getByLabel('İhtiyaç gerekçesi').fill('Sube icin10 temel birim');const need=await action('/needs',()=>page.getByRole('button',{name:'İhtiyacı kaydet',exact:true}).click());
  await action('/needs/'+need.id+'/approve',async()=>{await(await findOnPages(page,'/api/v1/needs','button','İhtiyacı onayla · '+need.id)).click()});await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();
  await login('depo@ddo-case.invalid');await menu('Siparişler');await page.getByLabel('Sipariş ürünü 1',{exact:true}).selectOption(p.id);await page.getByLabel('Sipariş gerekçesi').fill('Onayli ihtiyactan konsolide');await page.getByLabel(`İhtiyaç ${need.id} · Şube ${branch} · 10 temel birim`,{exact:true}).check();const order=await action('/orders',()=>page.getByRole('button',{name:'Sipariş taslağını kaydet',exact:true}).click());
  await action('/orders/'+order.id+'/approve',async()=>{await(await findOnPages(page,'/api/v1/orders','button','Siparişi onayla · '+order.id)).click()});
  await menu('Mal kabul');await page.getByLabel('Bağlı sipariş',{exact:true}).selectOption(order.id);await page.getByLabel('Ürün 1',{exact:true}).selectOption(p.id);await page.getByLabel('Lot 1',{exact:true}).fill(tag);await page.getByLabel('SKT 1',{exact:true}).fill('2099-12-31');await page.getByLabel('Kabul gerekçesi',{exact:true}).fill('Fiziksel kalite kontrolu');
  const remainders:string[]=[];
  for(const r of [{tag:'good4',q:'4',mode:'',want:'6'},{tag:'bad3',q:'3',mode:'quarantine',want:'6'},{tag:'good6',q:'6',mode:'',want:'0'}]){
   await page.getByLabel('İrsaliye numarası').fill(tag+'-'+r.tag);await page.getByLabel('Kabul durumu',{exact:true}).selectOption(r.mode);await page.getByLabel('Miktar 1',{exact:true}).fill(r.q);
   if(r.tag==='good6'){
    await page.getByLabel('Miktar 1',{exact:true}).fill('7');await action('/goods-receipts',()=>page.getByRole('button',{name:'İrsaliyeyi kabul et',exact:true}).click(),409);await expect(page.getByLabel('Miktar 1',{exact:true})).toHaveValue('7');await expect(page.getByLabel('İrsaliye numarası')).toHaveValue(tag+'-'+r.tag);await page.getByLabel('Miktar 1',{exact:true}).fill('6');
   }
   await action('/goods-receipts',()=>page.getByRole('button',{name:'İrsaliyeyi kabul et',exact:true}).click());
   const rows=await allRows<{id:string;lines:{remaining:string}[]}>(warehouse.api,'/api/v1/orders?location_id=21&q='+order.id);const remaining=rows.find(v=>v.id===order.id)!.lines[0].remaining;expect(remaining).toBe(r.want);remainders.push(remaining);
  }
  await menu('Tahsisler');const allocations=await allRows<{id:string;order_id:string;at_warehouse:string;in_transit:string;delivered:string}>(warehouse.api,'/api/v1/allocations?location_id=21');const a=allocations.find(v=>v.order_id===order.id)!;expect(a.at_warehouse).toBe('10');
  await(await findOnPages(page,'/api/v1/allocations','button','Tahsisli sevk hazırla · '+a.id)).click();const transfer=await action('/transfers',()=>page.getByRole('button',{name:'Taslak oluştur',exact:true}).click());
  await menu('Transfer');await action('/transfers/'+transfer.id+'/send',async()=>{await(await findOnPages(page,'/api/v1/transfers','button','Sevk et · '+transfer.id)).click()},200);
  await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();await login(pharmacist);await menu('Transfer');await action('/transfers/'+transfer.id+'/receive',async()=>{await(await findOnPages(page,'/api/v1/transfers','button','Sağlam tam teslim al · '+transfer.id)).click()},200);
  await menu('Tahsisler');await expect(page.getByRole('button',{name:'Tahsisli sevk hazırla',exact:false})).toHaveCount(0);await expect(page.getByText('Şubeye sağlam teslim: 10',{exact:false})).toBeVisible();await page.getByText('Kaynak sipariş '+order.id,{exact:true}).click();await expect(page.getByText('Ürün '+p.id+' · Miktar 10 · Açık kalan 0',{exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();await login('muhasebe@ddo-case.invalid');await menu('Siparişler');await expect(page.getByRole('button',{name:'Sipariş taslağını kaydet',exact:true})).toHaveCount(0);
  const forbidden=await page.request.post('/api/v1/orders',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':(await(await page.request.get('/api/v1/auth/csrf')).json()).csrf_token,'Idempotency-Key':randomUUID()},data:{location_id:'21',supplier_id:'1',reason:'Forbidden',order:{lines:[{product_id:p.id,quantity:'1'}]}}});expect(forbidden.status()).toBe(403);
  expect(errors).toEqual([]);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);await page.screenshot({path:`${evidence}/procurement-${width}.png`,fullPage:true});
  writeFileSync(`${evidence}/procurement-${width}.json`,JSON.stringify({branch,actors,product:p.id,need:need.id,order:order.id,allocation:a.id,transfer:transfer.id,remainders},null,2));
 }finally{db(`UPDATE "pl-wy-users" SET active=false WHERE id IN (${actors.join(',')}); UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id IN (${actors.join(',')});`);if(warehouse)await logout(warehouse)}
});
