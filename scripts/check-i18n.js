#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');

const root = path.resolve(__dirname, '..');
const i18nPath = path.join(root, 'web', 'i18n.js');
const htmlPath = path.join(root, 'web', 'index.html');

const context = {
  localStorage: { getItem() { return null; }, setItem() {} },
  document: { querySelectorAll() { return []; }, getElementById() { return null; } },
};

vm.createContext(context);
vm.runInContext(fs.readFileSync(i18nPath, 'utf8') + '\nthis.__DICT = DICT;', context, { filename: i18nPath });

const dict = context.__DICT;
const langs = Object.keys(dict);
const baseLang = langs[0];
const baseKeys = Object.keys(dict[baseLang]).sort();
let failed = false;

for (const lang of langs) {
  const keys = Object.keys(dict[lang]).sort();
  const missing = baseKeys.filter((key) => !keys.includes(key));
  const extra = keys.filter((key) => !baseKeys.includes(key));
  console.log(`${lang}: ${keys.length} keys, missing=${missing.length}, extra=${extra.length}`);
  if (missing.length || extra.length) {
    failed = true;
    if (missing.length) console.error(`missing in ${lang}: ${missing.join(', ')}`);
    if (extra.length) console.error(`extra in ${lang}: ${extra.join(', ')}`);
  }
}

const html = fs.readFileSync(htmlPath, 'utf8');
const refs = [
  ...[...html.matchAll(/data-i18n="([^"]+)"/g)].map((match) => match[1]),
  ...[...html.matchAll(/data-i18n-placeholder="([^"]+)"/g)].map((match) => match[1]),
];
const uniqueRefs = [...new Set(refs)].sort();
const missingRefs = uniqueRefs.filter((key) => langs.some((lang) => !dict[lang][key]));
console.log(`html refs=${uniqueRefs.length}, missing refs=${missingRefs.length}`);
if (missingRefs.length) {
  failed = true;
  console.error(`missing html refs: ${missingRefs.join(', ')}`);
}

process.exit(failed ? 1 : 0);
