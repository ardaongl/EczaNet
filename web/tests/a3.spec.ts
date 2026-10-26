import {test,expect,type Page} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
import {account,post,logout,type Account,password,db} from './support/fixtures';
import {allRows,findOnPages,type Sale} from './support/a2';
const evidence=process.env.A3_EVIDENCE_DIR??process.env.A2_EVIDENCE_DIR??'../artifacts/browser-runs/a3';
async function login(page:Page,email='seed.yildiz@ddo-case.invalid'){
 await page.goto('/');await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();await page.getByLabel('E-posta').fill(email);await page.getByLabel('Parola',{exact:true}).fill(password);await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.getByRole('heading',{name:'Çalışma alanı',exact:true})).toBeVisible();
}
async function product(w:Account,a:Account,prefix:string,pack:string,dose:string,group='',location='1'){
 const p=await post(w,'/products',{location_id:'21',product:{barcode:'00'+Date.now()+Math.floor(Math.random()*1e5),name:prefix+pack,dose,form:'tablet',base_unit:'tablet',pack_size:pack,blister_size:'5',sale_multiple:'1',ingredients:['a3-ui-demo'],prescription_required:true,controlled_group:group,pack_price:'20000'}});
 await post(w,'/goods-receipts',{location_id:'21',supplier_id:'1',document_no:prefix+pack,reason:'A3 retained browser fixture',lines:[{product_id:p.id,lot:prefix+pack,expires_on:'2099-12-31',quantity:'70'}]});
 const t=await post(w,'/transfers',{location_id:'21',target_id:location,items:[{product_id:p.id,quantity:'70',unit:'unit'}]});await post(w,'/transfers/'+t.id+'/send',{location_id:'21'},200);await post(a,'/transfers/'+t.id+'/receive',{location_id:location},200);return p.id as string;
}
async function prescription(a:Account,p:string,prefix:string,amount='30',location='1'){
 return post(a,'/prescriptions',{location_id:location,prescription:{external_id:prefix,patient_external_id:prefix+'H1',birth_date:'1990-01-01',issued_at:new Date(Date.now()-3600000).toISOString(),lines:[{prescribed_product_id:p,prescribed_base:amount}]}});
}
async function openRX(page:Page,external:string){
 await page.getByRole('navigation').getByRole('button',{name:'Kasa ve reçete',exact:true}).click();await page.getByLabel('Harici reçete kimliği',{exact:true}).fill(external);await page.getByRole('button',{name:'Reçeteyi aç',exact:true}).click();await expect(page.getByRole('heading',{name:'Reçete '+external,exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Sunucu teklifini al',exact:true})).toBeEnabled();
}
for(const width of [390,1280])test(`A3 reçete eşdeğer kısmi ve belirsiz sonuç ${width}`,async({page})=>{
 test.setTimeout(90000);const w=await account('depo@ddo-case.invalid'),a=await account('seed.yildiz@ddo-case.invalid');const prefix='A3-UI-'+randomUUID();
 try{
  const p=await product(w,a,prefix,'30','500 mg'),q=await product(w,a,prefix,'20','0.5 g');const rx=await prescription(a,p,prefix);
  await page.setViewportSize({width,height:1000});const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await login(page);await openRX(page,prefix);
  await page.getByLabel('Ürün 1',{exact:true}).selectOption(q);await page.getByLabel('Miktar 1',{exact:true}).fill('10');await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await expect(page.locator('.receipt').last()).toContainText('Hasta toplamı: 1800 kuruş · Kurum: 7200 kuruş');
  const requests:{key:string;body:string}[]=[];let dropped=false;await page.route('**/api/v1/sales',async route=>{requests.push({key:route.request().headers()['idempotency-key'],body:route.request().postData()!});if(!dropped){dropped=true;const r=await route.fetch();expect(r.status()).toBe(201);await route.abort()}else await route.continue()});
  await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).dblclick();await expect(page.getByRole('button',{name:'Sonucu kontrol et',exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'Sunucu teklifini al',exact:true})).toBeDisabled();
  await page.reload();await page.getByRole('navigation').getByRole('button',{name:'Kasa ve reçete',exact:true}).click();await page.getByRole('button',{name:'Sonucu kontrol et',exact:true}).click();await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toBeVisible();expect(requests).toHaveLength(2);expect(requests[1]).toEqual(requests[0]);
  await page.getByLabel('Harici reçete kimliği',{exact:true}).fill(prefix);await page.getByRole('button',{name:'Reçeteyi aç',exact:true}).click();await expect(page.locator('.receipt').filter({hasText:'Yazılan ürün'})).toContainText('Karşılanan 10 · Kalan 20');
  await page.getByLabel('Ürün 1',{exact:true}).selectOption(q);await page.getByLabel('Miktar 1',{exact:true}).fill('15');await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).click();await expect(page.locator('.receipt').filter({hasText:'Yazılan ürün'})).toContainText('Karşılanan 25 · Kalan 5');
  const sales=(await allRows<Sale>(a.api,'/api/v1/sales?location_id=1')).filter(s=>s.lines.some(l=>l.product_id===q));expect(sales).toHaveLength(2);expect(sales.map(s=>s.total)).toEqual(['9000','13325']);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(errors).toEqual([]);await page.screenshot({path:`${evidence}/a3-rx-${width}.png`,fullPage:true});writeFileSync(`${evidence}/a3-rx-${width}.json`,JSON.stringify({rx:rx.id,prescribed:p,dispensed:q,sales:sales.map(s=>s.id),remaining:'5',sameKey:true,requests:requests.length},null,2));
 }finally{await logout(w);await logout(a)}
});

test('A3 PRICE_CHANGED gerçek fiyat geçişi ve açık yeniden onay',async({page})=>{
 test.setTimeout(90000);const w=await account('depo@ddo-case.invalid'),a=await account('seed.yildiz@ddo-case.invalid'),acc=await account('muhasebe@ddo-case.invalid');const prefix='A3-PRICE-'+randomUUID();
 try{
  const p=await product(w,a,prefix,'20','500 mg');const rx=await prescription(a,p,prefix);await login(page);await openRX(page,prefix);await page.getByLabel('Miktar 1',{exact:true}).fill('20');await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await expect(page.locator('.receipt').last()).toContainText('3520 kuruş');
  const transition=new Date(Date.now()+2000);await post(acc,'/prices',{location_id:'1',price:{product_id:p,valid_from:transition.toISOString(),pack_price:'30000'}});
  await expect.poll(()=>db(`SELECT clock_timestamp()>=('${transition.toISOString()}'::timestamptz);`),{timeout:8000}).toBe('t');
  const sent:{key:string;body:string}[]=[];page.on('request',r=>{if(r.url().endsWith('/api/v1/sales')&&r.method()==='POST')sent.push({key:r.headers()['idempotency-key'],body:r.postData()!})});
  await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).click();await expect(page.getByRole('alert')).toContainText('Teklif degisti');await expect(page.getByRole('button',{name:'Satışı kesinleştir',exact:true})).toHaveCount(0);
  const zero=db(`SELECT count(*) FROM "pl-sales" WHERE prescription_id=${rx.id};`);expect(zero).toBe('0');expect(sent).toHaveLength(1);
  await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await expect(page.locator('.receipt').last()).toContainText('5160 kuruş');expect(sent).toHaveLength(1);
  await page.screenshot({path:`${evidence}/a3-price-reapproval.png`,fullPage:true});await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).click();await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toBeVisible();expect(sent).toHaveLength(2);expect(sent[0].key).not.toBe(sent[1].key);expect(db(`SELECT count(*) FROM "pl-sales" WHERE prescription_id=${rx.id};`)).toBe('1');
  writeFileSync(`${evidence}/a3-price-reapproval.json`,JSON.stringify({rx:rx.id,p,oldRejected:true,explicitReapproval:true,newKey:sent[0].key!==sent[1].key,total:'25800',patient:'5160',institution:'20640'},null,2));
 }finally{await logout(w);await logout(a);await logout(acc)}
});

test('A3 teknisyen hazırlığı ve muhasebe fiyat ekranı gerçek API',async({page})=>{
 test.setTimeout(90000);const w=await account('depo@ddo-case.invalid'),a=await account('seed.yildiz@ddo-case.invalid');const prefix='A3-ROLES-'+randomUUID();
 try{
  const p=await product(w,a,prefix,'20','500 mg');await prescription(a,p,prefix);await login(page,'teknisyen@ddo-case.invalid');await openRX(page,prefix);await page.getByLabel('Miktar 1',{exact:true}).fill('1');await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await expect(page.getByRole('button',{name:'Satışı kesinleştir',exact:true})).toBeDisabled();
  await page.getByRole('button',{name:'Çıkış yap',exact:true}).click();await login(page,'muhasebe@ddo-case.invalid');await page.getByRole('navigation').getByRole('button',{name:'Fiyat politikası',exact:true}).click();await page.getByLabel('Fiyat ürünü',{exact:true}).selectOption(p);await page.getByLabel('Fiyat başlangıcı',{exact:true}).fill('2090-01-01T00:00');await page.getByLabel('Yeni kutu fiyatı (kuruş)',{exact:true}).fill('25000');await page.getByRole('button',{name:'Fiyatı planla',exact:true}).click();await expect(page.getByRole('heading',{name:'İşlem tamamlandı',exact:true})).toBeVisible();await page.screenshot({path:`${evidence}/a3-accountant-price.png`,fullPage:true});
 }finally{await logout(w);await logout(a)}
});

test('A3 reçete yükleme formu ve kontrollü defter gerçek API',async({page})=>{
 test.setTimeout(90000);const prefix='A3-IMPORT-'+randomUUID();const email=prefix.toLowerCase()+'@ddo-case.invalid';
 const location=db(`INSERT INTO "pl-locations"(name,kind) VALUES('${prefix}','branch') RETURNING id;`).split('\n')[0];
 const uid=db(`INSERT INTO "pl-wy-users"(email,password_hash,role) SELECT '${email}',password_hash,'pharmacist' FROM "pl-wy-users" WHERE id=1 RETURNING id;`).split('\n')[0];
 db(`INSERT INTO "pl-user-locations" VALUES(${uid},${location});`);
 const w=await account('depo@ddo-case.invalid'),a=await account(email);
 try{
  const p=await product(w,a,prefix,'20','500 mg','CONTROL-TAB',location);
  // Each row is an actual dispensation by a different demo patient. No limit
  // bypass or direct ledger writes; these records and the branch are retained.
  for(let i=0;i<50;i++){
   const rx=await prescription(a,p,prefix+'-PAD-'+i,'1',location);
   const input={location_id:location,prescription_id:rx.id,dispense:[{prescription_line_id:rx.lines[0].id,dispensed_product_id:p,quantity:'1',unit:'unit'}]};
   const q=await post(a,'/sales/quote',input,200);await post(a,'/sales',{...input,quote:q.quote});
  }
  const ledgerPath='/api/v1/controlled-ledger?location_id='+location;
  expect(await allRows(a.api,ledgerPath)).toHaveLength(50);
  await login(page,email);await page.getByRole('navigation').getByRole('button',{name:'Kasa ve reçete',exact:true}).click();await page.getByLabel('Ürün 1',{exact:true}).selectOption(p);await page.getByLabel('Miktar 1',{exact:true}).fill('6');
  await page.getByText('Kurgusal reçete yükle',{exact:true}).click();await page.getByLabel('Yeni reçete kimliği',{exact:true}).fill(prefix);await page.getByLabel('Hasta demo kimliği',{exact:true}).fill(prefix+'H1');await page.getByLabel('Düzenlenme zamanı',{exact:true}).fill(new Date(Date.now()-3600000).toISOString().slice(0,16));
  const imported=page.waitForResponse(r=>r.url().endsWith('/api/v1/prescriptions')&&r.request().method()==='POST'&&r.status()===201);await page.getByRole('button',{name:'Demo reçeteyi yükle',exact:true}).click();const rx=await(await imported).json();await expect(page.getByRole('heading',{name:'Reçete '+prefix,exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Sunucu teklifini al',exact:true}).click();await page.getByLabel('Tahsilat yöntemi',{exact:true}).selectOption('card');await page.getByRole('button',{name:'Satışı kesinleştir',exact:true}).click();await expect(page.locator('.receipt').filter({hasText:'Yazılan ürün'})).toContainText('Kalan 0');
  expect(db(`SELECT count(*) FROM "pl-controlled-ledger" WHERE prescription_id=${rx.id} AND quantity=6;`)).toBe('1');expect(db(`SELECT payment_method FROM "pl-sales" WHERE prescription_id=${rx.id};`)).toBe('card');
  await page.getByRole('navigation').getByRole('button',{name:'Kontrollü defter',exact:true}).click();
  const ledgerID=db(`SELECT id FROM "pl-controlled-ledger" WHERE prescription_id=${rx.id};`);
  const rows=await allRows<{id:string}>(a.api,ledgerPath);expect(rows).toHaveLength(51);expect(new Set(rows.map(r=>r.id)).size).toBe(51);expect(rows.map(r=>r.id)).toEqual([...rows.map(r=>r.id)].sort((a,b)=>Number(a)-Number(b)));expect(rows.findIndex(r=>r.id===ledgerID)).toBe(50);
  await expect(page.locator('.records article')).toHaveCount(50);await expect(page.getByRole('heading',{name:'Belge '+ledgerID,exact:true})).toHaveCount(0);
  await findOnPages(page,'/api/v1/controlled-ledger','heading','Belge '+ledgerID);await expect(page.locator('.records article').filter({has:page.getByRole('heading',{name:'Belge '+ledgerID,exact:true})})).toContainText('Kontrol grubu: CONTROL-TAB');
  await page.screenshot({path:`${evidence}/a3-controlled-ledger.png`,fullPage:true});writeFileSync(`${evidence}/a3-import-control.json`,JSON.stringify({rx:rx.id,p,location,uid,ledgerID,baselineRows:50,totalRows:51,targetOrdinal:51,targetPage:2,importViaUI:true,controlledQuantity:'6',payment:'card',remaining:'0'},null,2));
 }finally{await logout(w);await logout(a);db(`UPDATE "pl-wy-users" SET active=false WHERE id=${uid}; UPDATE "pl-sessions" SET revoked_at=clock_timestamp() WHERE user_id=${uid};`)}
});
