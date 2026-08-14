import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const source = readFileSync(resolve(dir, '../VersionBadge.vue'), 'utf8')

describe('VersionBadge 回退目标', () => {
  it('手动命令始终指向 TokenRouter 仓库与 GHCR 镜像', () => {
    expect(source).toContain("const GITHUB_REPO = 'BrandonVee/TokenRouter'")
    expect(source).toContain("const DOCKER_IMAGE = 'ghcr.io/brandonvee/tokenrouter'")
    expect(source).not.toContain("const GITHUB_REPO = 'Wei-Shaw/sub2api'")
    expect(source).not.toContain("const DOCKER_IMAGE = 'weishaw/sub2api'")
  })
})
