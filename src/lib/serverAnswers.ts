// Test-only: what the server answers for the repo-pattern and settings checks, read from testdata/answers/. The Go server's tests
// hold it to the same files, so the page and the server agree on every case. A call that is not in a file throws: add the case there.
import repomatch from '../../testdata/answers/repomatch.json'
import settings from '../../testdata/answers/settings.json'

type Case = { args: unknown[]; out: unknown }

function answer(file: string, cases: Case[], args: unknown[]): unknown {
  const key = JSON.stringify(args)
  const hit = cases.find(c => JSON.stringify(c.args) === key)
  if (!hit) throw new Error(`no answer for ${key} in testdata/answers/${file}`)
  return hit.out
}

export const parsePattern = (raw: string) =>
  answer('repomatch.json', repomatch.parsePattern, [raw]) as { ok: true; pattern: string } | { ok: false; reason: string }
export const allowed = (repo: string, list: string[] | null) => answer('repomatch.json', repomatch.allowed, [repo, list]) as boolean
export const parseBranch = (raw: string) => answer('settings.json', settings.parseBranch, [raw]) as string | null
export const validateSection = (section: string, value: unknown) =>
  answer('settings.json', settings.validateSection, [section, value]) as { ok: boolean; value?: unknown; error?: string }
