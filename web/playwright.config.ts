import { defineConfig } from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {mkdirSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
const evidence=fileURLToPath(new URL('../artifacts/browser-runs/'+new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomUUID()+'/',import.meta.url));
mkdirSync(evidence,{recursive:true});
for(const key of ['A2_EVIDENCE_DIR','A3_EVIDENCE_DIR','A4_EVIDENCE_DIR','A5_EVIDENCE_DIR','A6_EVIDENCE_DIR'])process.env[key]??=evidence;
export default defineConfig({testDir:'./tests',workers:1,use:{baseURL:process.env.TEST_WEB_URL??'http://localhost:3000',headless:true},reporter:'list'});
