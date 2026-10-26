import { test, expect } from '@playwright/test';

for (const width of [390,1280]) {
 test(`giriş / lokasyon / rol / çıkış ${width}px`,async({page})=>{
  await page.setViewportSize({width,height:900});
  await page.goto('/');
  await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
  await page.getByLabel('E-posta').fill('seed.yildiz@ddo-case.invalid');
  await page.getByLabel('Parola',{exact:true}).fill('incorrect');
  await page.getByRole('button',{name:'Giriş yap',exact:true}).click();
  await expect(page.getByRole('alert')).toContainText('E-posta veya parola');
  await expect(page.getByLabel('E-posta')).toHaveValue('seed.yildiz@ddo-case.invalid');
  await page.getByLabel('Parola',{exact:true}).fill(process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!');
  await page.getByRole('button',{name:'Giriş yap',exact:true}).click();
  await expect(page.getByRole('heading',{name:'Çalışma alanı',exact:true})).toBeVisible();
  await expect(page.getByLabel('Aktif lokasyon')).toHaveValue('1');
  await expect(page.getByRole('navigation')).not.toContainText('Mal kabul');
  const blocked=await page.request.get('/api/v1/locations/2');
  expect(blocked.status()).toBe(403);expect(blocked.headers()['x-ddo-stamp']).toBe('a33906');
  const current=await page.request.get('/api/v1/auth/me');expect(current.status()).toBe(200);
  expect((await page.context().cookies()).find(c=>c.name==='eczanet_session')?.httpOnly).toBe(true);
  await page.reload();await expect(page.getByLabel('Aktif lokasyon')).toHaveValue('1');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.screenshot({path:`${process.env.A2_EVIDENCE_DIR??"../artifacts/browser-runs/auth"}/auth-ui-${width}.png`,fullPage:true});
  await page.getByRole('button',{name:'Çıkış yap'}).click();
  await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
  expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401);
 });
}
test('muhasebe lokasyon boş durumu / menü',async({page})=>{
 await page.goto('/');await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
 await page.getByLabel('E-posta').fill('muhasebe@ddo-case.invalid');await page.getByLabel('Parola',{exact:true}).fill(process.env.DEMO_PASSWORD??'Demo-EczaNet-2026!');await page.getByRole('button',{name:'Giriş yap',exact:true}).click();
 await expect(page.getByText('Atanmış fiziksel lokasyonunuz yok.',{exact:false})).toBeVisible();
 await expect(page.getByRole('navigation')).toContainText('Finans raporları');await expect(page.getByRole('navigation')).not.toContainText('Kasa');
});
test('bağlantı hatası görünür ve yeniden deneme çalışır',async({page})=>{
 await page.route('**/api/v1/auth/csrf',route=>route.abort());
 await page.goto('/');
 await expect(page.getByRole('alert')).toContainText('Giriş hazırlığı yapılamadı');
 await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeDisabled();
 await page.unroute('**/api/v1/auth/csrf');
 await page.getByRole('button',{name:'Yeniden dene'}).click();
 await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
});
test('başlangıçta bekleyen sorgu yükleniyor gösterir',async({page})=>{
 let release!:()=>void;const gate=new Promise<void>(resolve=>{release=resolve});
 await page.route('**/api/v1/auth/me',async route=>{await gate;await route.continue()});
 await page.goto('/');await expect(page.getByRole('status')).toContainText('yükleniyor');
 release();await expect(page.getByRole('button',{name:'Giriş yap',exact:true})).toBeEnabled();
});
