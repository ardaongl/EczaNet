import {test,expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {account,logout,post,db,password,type Account} from './support/fixtures';
const evidence=process.env.A4_EVIDENCE_DIR??fileURLToPath(new URL('../../artifacts/browser-runs/a4/',import.meta.url));
for(const width of [390,1280])test(`A4 iade karantina transfer sayım ${width}`,async({page})=>{
 test.setTimeout(120000);await page.setViewportSize({width,height:1000});
 const tag='a4-ui-'+randomUUID();const branch=db(`INSERT INTO "pl-locations"(name,kind) VALUES('${tag}','branch') RETURNING id;`).split('\n')[0];
 const actors:string[]=[];let warehouse:Account|undefined,receiver:Account|undefined;
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 function actor(role:string){const email=tag+'-'+role+'@ddo-case.invalid';const id=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${email}',password_hash,'${role}' FROM "pl-wy-users" WHERE id=1 RETURNING id;`).split('\n')[0];actors.push(id);db(`INSERT INTO "pl-user-locations" VALUES(${id},${branch});`);return email}
 const pharmacist=actor('pharmacist'),manager=actor('manager');
 async function login(email:string){await page.goto('/');await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();await page.getByLabel('E-posta').fill(email);await page.getByLabel('Parola',{exact:true}).fill(password);await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.getByRole('heading',{name:'Çalışma alanı',exact:true})).toBeVisible()}
 async function menu(name:string){await page.getByRole('navigation').getByRole('button',{name,exact:true}).click();await expect(page.getByRole('status').filter({hasText:'Veriler yükleniyor'})).toHaveCount(0)}
 async function action(path:string,click:()=>Promise<unknown>,status=201){const response=page.waitForResponse(r=>r.request().method()==='POST'&&new URL(r.url()).pathname==='/api/v1'+path);await click();const result=await response;expect(result.status(),await result.text()).toBe(status);return result.json()}
 try{
  warehouse=await account('depo@ddo-case.invalid');receiver=await account(pharmacist);
  const barcode='00'+Date.now()+String(width);
  const product=await post(warehouse,'/products',{location_id:'21',product:{barcode,name:tag,dose:'5mg',form:'tablet',base_unit:'tablet',pack_size:'10',blister_size:'0',sale_multiple:'1',ingredients:['a4-browser'],prescription_required:false,controlled_group:'',pack_price:'1000'}});
  await post(warehouse,'/goods-receipts',{location_id:'21',supplier_id:'1',document_no:tag,reason:'A4 retained browser fixture',lines:[{product_id:product.id,lot:tag,expires_on:'2099-12-31',quantity:'10'}]});
  const transfer=await post(warehouse,'/transfers',{location_id:'21',target_id:branch,items:[{product_id:product.id,quantity:'10',unit:'unit'}]});await post(warehouse,'/transfers/'+transfer.id+'/send',{location_id:'21'},200);
  await login(pharmacist);await menu('Transfer');
  const delivery=page.getByRole('group',{name:`Transfer ${transfer.id} teslimi`,exact:true});
  await delivery.getByLabel('Sağlam',{exact:true}).fill('4');await action('/transfers/'+transfer.id+'/receive',()=>delivery.getByRole('button',{name:'Ara teslimi kaydet'}).click(),200);
  await expect(page.getByText('PARTIALLY_RECEIVED',{exact:false}).first()).toBeVisible();
  await delivery.getByLabel('Teslim türü').selectOption('final');await delivery.getByLabel('Sağlam',{exact:true}).fill('4');await delivery.getByLabel('Hasarlı / uygunsuz').fill('1');await delivery.getByLabel('Nihai eksik').fill('1');await delivery.getByLabel('Teslim farkı gerekçesi').fill('Bir hasar ve bir eksik fiziksel kontrol');await action('/transfers/'+transfer.id+'/receive',()=>delivery.getByRole('button',{name:'Nihai teslimi kesinleştir'}).click(),200);
  const lot=db(`SELECT lot_id FROM "pl-transfer-lines" WHERE transfer_id=${transfer.id};`).split('\n')[0];
  const input={location_id:branch,items:[{product_id:product.id,quantity:'3',unit:'unit'}]};const quote=await post(receiver,'/sales/quote',input,200);const sale=await post(receiver,'/sales',{...input,quote:quote.quote});
  await menu('Satışlar');await page.getByLabel('Asıl satış numarası').fill(sale.id);await page.getByRole('button',{name:'Satışı aç',exact:true}).click();await expect(page.getByRole('heading',{name:'Kaynak satış '+sale.id,exact:true})).toBeVisible();
  await page.getByLabel('İade gerekçesi').fill('Muhurlu urun fiziksel iade');await page.getByRole('button',{name:'İadeyi önizle'}).click();await expect(page.getByText('İade toplamı: 100 kuruş',{exact:false})).toBeVisible();
  const returned=await action('/returns',()=>page.getByRole('button',{name:'İadeyi karantinaya kesinleştir'}).click());
  await menu('Karantina');const decision=page.getByRole('group',{name:`Karantina ${returned.quarantine_id} değerlendirmesi`,exact:true});await decision.getByLabel('Karar gerekçesi').fill('Muhurlu lot ve SKT kontrolu');await decision.getByLabel('Mühür, fiziksel lot ve SKT doğrulandı').check();await action('/quarantine/'+returned.quarantine_id+'/decide',()=>decision.getByRole('button',{name:'Gerekçeli kararı kaydet'}).click());
  await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();await login(manager);await menu('Sayım');await page.getByLabel('Sayım ürünü').selectOption(product.id);const count=await action('/counts',()=>page.getByRole('button',{name:'30 dakikalık sayımı başlat'}).click());
  const extraLot=db(`INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES(${product.id},1,'${tag}-extra','2099-12-31') RETURNING id;`).split('\n')[0];
  const counting=page.getByRole('group',{name:`Sayım ${count.id} — Ürün donduruldu`,exact:true});await expect(counting.getByText('Kalan süre:',{exact:false})).toBeVisible();await counting.getByLabel('Ek fiziksel lot numarası').fill(extraLot);await counting.getByRole('button',{name:'Ek lotu sayıma ekle',exact:true}).click();await expect(counting.getByLabel(`Lot ${extraLot} / physical fiziksel sayım`)).toHaveValue('0');await counting.getByLabel(`Lot ${lot} / physical fiziksel sayım`).fill('4');await counting.getByLabel('Sayım bitirme / iptal gerekçesi').fill('Beklenen6 fiziksel4');await action('/counts/'+count.id+'/finalize',()=>counting.getByRole('button',{name:'Sayım farkını kesinleştir'}).click());
  await expect(page.getByText('COMPLETED',{exact:false}).first()).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);expect(errors).toEqual([]);
  await page.screenshot({path:`${evidence}/a4-ui-${width}.png`,fullPage:true});
  const totals=db(`SELECT json_build_object('physical',(SELECT quantity FROM "pl-stock-balances" WHERE location_id=${branch} AND lot_id=${lot} AND account='physical'),'quarantine',(SELECT quantity FROM "pl-stock-balances" WHERE location_id=${branch} AND lot_id=${lot} AND account='quarantine'),'count_counter',(SELECT quantity FROM "pl-stock-balances" WHERE location_id=${branch} AND lot_id=${lot} AND account='count'),'loss',(SELECT quantity FROM "pl-stock-balances" WHERE location_id=21 AND lot_id=${lot} AND account='loss'));`);
  expect(JSON.parse(totals)).toEqual({physical:4,quarantine:1,count_counter:2,loss:1});writeFileSync(`${evidence}/a4-ui-${width}.json`,JSON.stringify({branch,actors,product:product.id,transfer:transfer.id,sale:sale.id,returned,count,totals:JSON.parse(totals)},null,2));
 }finally{
  // Only this run's new accounts/count scopes are touched; historical fixtures remain.
  db(`UPDATE "pl-counts" SET status='CANCELLED' WHERE location_id=${branch} AND status='OPEN'; UPDATE "pl-wy-users" SET active=false WHERE id IN (${actors.join(',')}); UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id IN (${actors.join(',')});`);
  if(receiver)await logout(receiver);if(warehouse)await logout(warehouse);
 }
});

test('A4 gerçek HTTP rol, şube ve istek korumaları',async()=>{
 const warehouse=await account('depo@ddo-case.invalid'),technician=await account('teknisyen@ddo-case.invalid'),accountant=await account('muhasebe@ddo-case.invalid');
 try{
  for(const a of [warehouse,technician,accountant]){const r=await a.api.post('/api/v1/returns',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':a.csrf,'Idempotency-Key':randomUUID()},data:{location_id:a===warehouse?'21':'1',reason:'forbidden',return:{sale_id:'1',sale_line_id:'1',lot_id:'1',quantity:'1'}}});expect(r.status()).toBe(403);expect((await r.json()).error.code).toBe('FORBIDDEN')}
  const noCsrf=await warehouse.api.post('/api/v1/counts',{headers:{Origin:'http://localhost:3000'},data:{location_id:'21',count:{product_id:'1'}}});expect(noCsrf.status()).toBe(403);
  const invalid=await warehouse.api.post('/api/v1/counts',{headers:{Origin:'http://localhost:3000','X-CSRF-Token':warehouse.csrf,'Idempotency-Key':randomUUID()},data:{location_id:'21',count:{product_id:'1'},unexpected:true}});expect(invalid.status()).toBe(400);
 }finally{await logout(warehouse);await logout(technician);await logout(accountant)}
});
