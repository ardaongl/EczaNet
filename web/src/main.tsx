import { StrictMode, useEffect, useState, type FormEvent } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';
import {Business} from './business';
import {Dashboard} from './dashboard';

type User = {id:string;email:string;role:string};
type Location = {id:string;name:string;kind:string};
const roles:Record<string,string>={pharmacist:'Eczacı',technician:'Teknisyen',warehouse:'Depo sorumlusu',manager:'Şube müdürü',accountant:'Muhasebe'};
const planned:Record<string,string[]>={pharmacist:['Katalog','Stok','Hareketler','Satışlar','İadeler','Kasa ve reçete','Kontrollü defter','Transfer','Sayım','Karantina','İhtiyaçlar','Siparişler','Tahsisler','Öneriler','Raporlar','Kritik stok','Stok uzlaştırma','Transfer farkları','Finans raporları'],technician:['Katalog','Stok','Hareketler','Satışlar','İadeler','Kasa ve reçete'],warehouse:['Katalog','Stok','Hareketler','Mal kabul','Transfer','Sayım','Karantina','İhtiyaçlar','Siparişler','Tahsisler','Öneriler','Raporlar','Kritik stok','Stok uzlaştırma','Transfer farkları'],manager:['Katalog','Stok','Hareketler','Satışlar','Kontrollü defter','Transfer','Sayım','İhtiyaçlar','Siparişler','Tahsisler','Öneriler','Raporlar','Kritik stok','Stok uzlaştırma','Transfer farkları','Finans raporları'],accountant:['Öneriler','Katalog','Siparişler','Tahsisler','Finans raporları','Fiyat politikası']};
const demoAccounts=[
 {role:'Eczacı',email:'seed.yildiz@ddo-case.invalid'},
 {role:'Teknisyen',email:'teknisyen@ddo-case.invalid'},
 {role:'Depo sorumlusu',email:'depo@ddo-case.invalid'},
 {role:'Şube müdürü',email:'mudur@ddo-case.invalid'},
 {role:'Muhasebe',email:'muhasebe@ddo-case.invalid'}
];
const defaultDemoPassword='Demo-EczaNet-2026!';
const groups: {title:string; pages:string[]}[] = [
 {title:'Satış ve reçete',pages:['Kasa ve reçete','Satışlar','İadeler','Kontrollü defter']},
 {title:'Ürün ve stok',pages:['Katalog','Stok','Hareketler','Sayım','Karantina']},
 {title:'Tedarik ve sevkiyat',pages:['İhtiyaçlar','Siparişler','Mal kabul','Tahsisler','Transfer','Öneriler']},
 {title:'Raporlar ve finans',pages:['Raporlar','Kritik stok','Stok uzlaştırma','Transfer farkları','Finans raporları','Fiyat politikası']}
];
const guidance:Record<string,string>={
 'Katalog':'Ürünleri barkod, ad veya etken maddeyle bulun ve ürün bilgilerini inceleyin.',
 'Stok':'Seçili lokasyondaki ürünlerin lotlarını ve kullanılabilir stoklarını inceleyin.',
 'Hareketler':'Stok giriş ve çıkışlarını, ilgili hesap ve belgelerle takip edin.',
 'Kasa ve reçete':'Reçeteli veya reçetesiz satış hazırlayın; önce teklif alın, ardından satışı kesinleştirin.',
 'Satışlar':'Satış kayıtlarını inceleyin ve gerektiğinde kaynak satış üzerinden iade hazırlayın.',
 'İadeler':'İade belgelerini ve karantinaya alınan ürünleri takip edin.',
 'Kontrollü defter':'Kontrollü ürün işlemlerini ve günlük tüketim kayıtlarını inceleyin.',
 'Sayım':'Ürün sayımı başlatın, fiziksel miktarları girin ve farkı kesinleştirin.',
 'Karantina':'Uygun olmayan veya iade edilen ürünleri inceleyin ve gerekçeli karar verin.',
 'İhtiyaçlar':'Şube ihtiyacı oluşturun ve onay durumunu takip edin.',
 'Siparişler':'Tedarik siparişlerini ve açık kalan miktarlarını takip edin.',
 'Mal kabul':'İrsaliyeyi ve gelen ürünlerin lot ile son kullanma bilgilerini kaydedin.',
 'Tahsisler':'Bekleyen tedarikin şubelere ayrılmasını ve sevkiyat durumunu takip edin.',
 'Transfer':'Şubeler arası sevkiyatları hazırlayın, gönderin ve teslim alın.',
 'Öneriler':'Hesaplanan stok ihtiyaçlarını inceleyin ve uygun önerilerden taslak oluşturun.',
 'Raporlar':'Son kullanma tarihiyle ilgili riskleri inceleyin.',
 'Kritik stok':'Stok ihtiyacı olan ürünleri ve hesaplanan önerileri inceleyin.',
 'Stok uzlaştırma':'Stok hesapları arasındaki kayıtları karşılaştırın.',
 'Transfer farkları':'Sevkiyat ve teslimat arasındaki farkları inceleyin.',
 'Finans raporları':'Yetkiniz kapsamındaki finansal kayıtları inceleyin.',
 'Fiyat politikası':'Ürün fiyatlarını inceleyin ve gelecekte geçerli olacak fiyatları planlayın.'
};
const steps:Record<string,string[]>={
 'Kasa ve reçete':['Reçete kimliğiyle arayın veya reçetesiz devam edin.','Ürünleri ve miktarları seçip sunucu teklifini alın.','Tutarı kontrol ederek satışı kesinleştirin.'],
 'Stok':['Aktif lokasyonu kontrol edin.','Ürün ve lot bakiyelerini inceleyin.','Gerekirse hareketler veya sayım ekranına geçin.'],
 'Transfer':['Hedef şube ve ürünleri seçerek taslak oluşturun.','Taslağı sevk edin.','Hedef lokasyonda teslim miktarlarını kaydedin.'],
 'Siparişler':['Onaylı ihtiyaçları inceleyin.','Depo hesabıyla sipariş taslağı oluşturun ve onaylayın.','Gelen ürünleri mal kabul ekranından kaydedin.'],
 'Mal kabul':['Tedarikçiyi ve varsa bağlı siparişi seçin.','İrsaliye, ürün, lot ve son kullanma tarihini girin.','Bilgileri kontrol edip kabulü kaydedin.'],
 'Sayım':['Ürünü seçip sayımı başlatın.','Süre dolmadan fiziksel miktarları girin.','Gerekçeyi yazarak farkı kesinleştirin.'],
 'İadeler':['Kaynak satış kaydını satışlar ekranında bulun.','İade miktarını önizleyin ve kesinleştirin.','Ürünü karantina ekranında değerlendirin.']
};
class APIError extends Error { constructor(public status:number,message:string){super(message)} }
async function request<T>(path:string,init?:RequestInit):Promise<T>{
 const response=await fetch('/api/v1'+path,{credentials:'same-origin',...init});
 if(!response.ok){const body=await response.json().catch(()=>null);throw new APIError(response.status,body?.error?.message??'Sunucuya ulaşılamıyor. Tekrar deneyin.');}
 return response.status===204?undefined as T:response.json();
}
function App(){
 const [user,setUser]=useState<User|null>(null),[locations,setLocations]=useState<Location[]>([]),[active,setActive]=useState<Location|null>(null);
 const [email,setEmail]=useState(''),[password,setPassword]=useState(''),[csrf,setCsrf]=useState(''),[loading,setLoading]=useState(true),[busy,setBusy]=useState(false),[error,setError]=useState(''),[page,setPage]=useState('Çalışma alanı');
 async function load(){setLoading(true);setError('');try{
  const me=await request<{user:User}>('/auth/me');setUser(me.user);
  const result=await request<{locations:Location[]}>('/auth/locations');setLocations(result.locations);setActive(result.locations[0]??null);
  const prep=await request<{csrf_token:string}>('/auth/csrf');setCsrf(prep.csrf_token);
 }catch(e){if(e instanceof APIError&&e.status===401){setUser(null);try{setCsrf((await request<{csrf_token:string}>('/auth/csrf')).csrf_token)}catch{setError('Giriş hazırlığı yapılamadı. Yeniden deneyin.')}}else{setError(e instanceof Error?e.message:'Bağlantı hatası')}}finally{setLoading(false)}}
 useEffect(()=>{void load()},[]);
 async function login(event:FormEvent){event.preventDefault();setBusy(true);setError('');try{
  await request('/auth/login',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({email,password})});setPassword('');await load();
 }catch(e){setError(e instanceof Error?e.message:'Giriş başarısız');try{setCsrf((await request<{csrf_token:string}>('/auth/csrf')).csrf_token)}catch{/* visible original error is retained */}}finally{setBusy(false)}}
 async function logout(){setBusy(true);setError('');try{await request('/auth/logout',{method:'POST',headers:{'X-CSRF-Token':csrf}});setUser(null);setLocations([]);setActive(null);setPage('Çalışma alanı');await load()}catch(e){if(e instanceof APIError&&e.status===401){setUser(null);await load()}else setError('Çıkış tamamlanamadı. Yeniden deneyin.')}finally{setBusy(false)}}
 async function selectLocation(id:string){setBusy(true);setError('');try{setActive(await request<Location>('/locations/'+id))}catch(e){if(e instanceof APIError&&e.status===401){setUser(null);await load()}else setError('Lokasyon açılamadı. Yeniden deneyin.')}finally{setBusy(false)}}
 if(loading)return <main className="loading" role="status">Çalışma alanı yükleniyor…</main>;
 const available=planned[user?.role??'']??[];
 const quick=(user?.role==='technician'?['Kasa ve reçete','Katalog','Stok']:user?.role==='warehouse'?['Mal kabul','Siparişler','Stok','Transfer']:user?.role==='accountant'?['Finans raporları','Fiyat politikası','Siparişler']:['Kasa ve reçete','Stok','İhtiyaçlar','Kritik stok']).filter(n=>available.includes(n));
 return <div className="app">{!user?<><header className="login-header"><a className="brand" href="/">Ecza<span>Net</span></a><span className="demo">Kurgusal demo</span></header><main className="login"><p className="eyebrow">ECZANE ZİNCİRİ YÖNETİMİ</p><h1>Tekrar hoş geldiniz</h1><p>Çalışma alanınıza erişmek için hesabınızla giriş yapın.</p><form onSubmit={login}><label htmlFor="email">E-posta</label><input id="email" type="email" autoComplete="username" value={email} onChange={e=>setEmail(e.target.value)} required/><label htmlFor="password">Parola</label><input id="password" type="password" autoComplete="current-password" value={password} onChange={e=>setPassword(e.target.value)} required/>{error&&<p role="alert" className="error">{error}</p>}<button disabled={busy||!csrf}>{busy?'Giriş yapılıyor…':'Giriş yap'}</button>{!csrf&&<button type="button" className="secondary" onClick={()=>void load()}>Yeniden dene</button>}</form><section className="demo-accounts" aria-labelledby="demo-accounts-title"><h2 id="demo-accounts-title">Demo hesapları</h2><p>Bir rol seçerek giriş bilgilerini doldurun.</p><div className="demo-account-list">{demoAccounts.map(account=><button type="button" className="demo-account" key={account.email} onClick={()=>{setEmail(account.email);setPassword(defaultDemoPassword);setError('')}}><strong>{account.role}</strong><span>{account.email}</span></button>)}</div><p className="note">Varsayılan demo parolası kullanılır. Parolayı yapılandırmada değiştirdiyseniz girişten önce güncelleyin.</p></section><p className="note">Bu sistem kurgusal veri kullanır. Gerçek SGK, MEDULA veya ödeme entegrasyonu içermez.</p></main></>:
 <div className="workspace"><aside><a className="brand" href="/">Ecza<span>Net</span></a><p className="side-caption">ECZANE YÖNETİMİ</p><nav aria-label="Çalışma menüsü"><button className={page==='Çalışma alanı'?'selected':''} aria-current={page==='Çalışma alanı'?'page':undefined} onClick={()=>setPage('Çalışma alanı')}><span className="nav-icon" aria-hidden="true">⌂</span>Çalışma alanı</button>{groups.map(group=>{const pages=group.pages.filter(name=>available.includes(name));return pages.length?<div className="nav-group" key={group.title}><p>{group.title}</p>{pages.map(name=><button key={name} className={page===name?'selected':''} aria-current={page===name?'page':undefined} onClick={()=>setPage(name)}><span className="nav-dot" aria-hidden="true"/>{name}</button>)}</div>:null})}</nav><div className="account"><strong>{roles[user.role]??user.role}</strong><span className="email">{user.email}</span><button className="secondary" disabled={busy} onClick={()=>void logout()}>Çıkış yap</button></div></aside><main className="content"><div className="content-top"><div><p className="breadcrumb">EczaNet <span>/</span> {page}</p><h1>{page}</h1></div><section className="location"><label htmlFor="location">Aktif lokasyon</label>{locations.length?<select id="location" value={active?.id??''} disabled={busy} onChange={e=>void selectLocation(e.target.value)}>{locations.map(l=><option key={l.id} value={l.id}>{l.name}</option>)}</select>:<p>Zincir finansı</p>}</section></div>{error&&<p role="alert" className="error">{error}</p>}{page==='Çalışma alanı'?<><div className="welcome"><div><p className="eyebrow">ÇALIŞMA ALANINIZ</p><h2>Gününüzü buradan yönetin</h2><p>{active?`${active.name} için `:''}satış, stok ve diğer işlemlere hızlıca ulaşın.</p></div><span className="welcome-mark" aria-hidden="true">✚</span></div><Dashboard onReauthenticate={()=>void load()}/><section className="quick-section"><div className="section-heading"><div><h2>Hızlı erişim</h2><p>Görevinize uygun sık kullanılan ekranlar</p></div></div><div className="quick-grid">{quick.map(name=><button key={name} className="quick-card" onClick={()=>setPage(name)}><span className="quick-arrow" aria-hidden="true">↗</span><strong>{name}</strong><span>{guidance[name]}</span></button>)}</div></section></>:<><div className="page-intro"><p>{guidance[page]??'Bu ekrandaki kayıtları ve işlemleri inceleyin.'}</p>{steps[page]&&<details><summary>Nasıl kullanılır?</summary><ol>{steps[page].map(step=><li key={step}>{step}</li>)}</ol></details>}</div><Business key={user.id+':'+page+':'+active?.id} page={page} location={active?.id??''} role={user.role} csrf={csrf} actor={user.id} onReauthenticate={()=>void load()}/></>}<footer>Kurgusal demo · Gerçek SGK / ödeme entegrasyonu yoktur.</footer></main></div>}</div>;
}
createRoot(document.getElementById('root')!).render(<StrictMode><App/></StrictMode>);
