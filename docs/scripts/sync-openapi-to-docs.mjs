#!/usr/bin/env node
/**
 * Copies the bundled OpenAPI spec to the ReportPortal docs repository.
 * Run from service-marketplace/docs: npm run sync:openapi
 */
import { copyFileSync, mkdirSync, existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const docsDir = resolve(__dirname, '..');
const serviceRoot = resolve(docsDir, '..');
const bundled = join(docsDir, 'openapi/dist/service-marketplace-v1.yaml');
const docsRepo = process.env.DOCS_REPO ?? resolve(serviceRoot, '../docs');
const target = join(docsRepo, 'apis/v1/service-marketplace.yaml');

if (!existsSync(bundled)) {
  console.error(`Bundled spec not found: ${bundled}`);
  console.error('Run: npm run bundle:openapi');
  process.exit(1);
}

mkdirSync(dirname(target), { recursive: true });
copyFileSync(bundled, target);
console.log(`Synced OpenAPI spec to ${target}`);
