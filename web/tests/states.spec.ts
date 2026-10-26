import {test,expect} from '@playwright/test';
import {mkdirSync} from 'node:fs';
import {join} from 'node:path';
const groups:Record<string,string[]>={depo:['Katalog','Stok','Hareketler','Mal kabul','Transfer','Sayım','Karantina','İhtiyaçlar','Siparişler','Tahsisler','Öneriler','Raporlar','Kritik stok','Stok uzlaştırma','Transfer farkları'],'seed.yildiz':['Satışlar','İadeler','Kasa ve reçete','Kontrollü defter','Finans raporları'],muhasebe:['Fiyat politikası']};
for(const [account,pages] of Object.entries(groups))for(const width of [390,1280])test(`A6 all data page states ${account} ${width}`,async({page})=>{
 test.setTimeout(90000);await page.setViewportSize({width,height:900});await page.goto('/');await page.getByLabel('E-posta',{exact:true}).fill(account+'@ddo-case.invalid');await page.getByLabel('Parola',{exact:true}).fill(process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!');await page.getByRole('button',{name:'Giriş yap',exact:true}).click();await expect(page.getByRole('button',{name:'Çıkış yap'})).toBeVisible();
 if(account==='depo')await expect(page.getByRole('button',{name:'Finans raporları',exact:true})).toHaveCount(0);
 const out=process.env.A6_EVIDENCE_DIR;if(out){mkdirSync(out,{recursive:true});await page.screenshot({path:join(out,`dashboard-${account}-${width}.png`),fullPage:true})}
 let mode:'error'|'empty'|'full'='error';
 await page.route('**/api/v1/**',async r=>{if(r.request().method()!=='GET'||r.request().url().includes('/auth/')||r.request().url().endsWith('/dashboard'))return r.continue();await new Promise(resolve=>setTimeout(resolve,250));if(mode==='error')return r.fulfill({status:503,json:{error:{code:'UNAVAILABLE',message:'UI durumu kontrollü hata'}}});return r.fulfill({json:{items:mode==='empty'?[]:[{id:'901',name:'UI state fixture',barcode:'000901',base_unit:'tablet',pack_size:'10',blister_size:'5',sale_multiple:'1',ingredients:['demo'],dose:'5mg',form:'tablet',active:true}],next_cursor:''}})});
 for(const name of pages){
 mode='error';await page.getByRole('button',{name,exact:true}).click();const content=page.locator('.business');await expect(content.getByRole('status')).toContainText('Veriler yükleniyor');await expect(content.getByRole('alert')).toContainText('UI durumu kontrollü hata');
 mode='empty';await content.getByRole('button',{name:'Yeniden yükle',exact:true}).click();await expect(content.getByText(['Mal kabul','Kasa ve reçete'].includes(name)?'Seçilebilir ürün bulunamadı.':'Kayıt bulunamadı.',{exact:true})).toBeVisible();
 // Trigger a real refresh through navigation; no mutation commands are sent.
 await page.getByRole('button',{name:'Çalışma alanı',exact:true}).click();mode='full';await page.getByRole('button',{name,exact:true}).click();await expect(content.getByRole('status')).toHaveCount(0);if(['Mal kabul','Kasa ve reçete'].includes(name))await expect(content.locator('option').filter({hasText:'UI state fixture'}).first()).toHaveCount(1);else await expect(content.getByRole('heading',{name:'UI state fixture',exact:true})).toBeVisible();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 }
});
