/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Link } from '@tanstack/react-router'
import { Coins, Grid2x2Check, ScanSearch } from 'lucide-react'
/*
 * 扩展设置（管理员可见）：本二开系列新增功能的统一设置入口。
 *
 * 把散落在各处的 hc 系列设置聚合成一个页面：
 *  - 导航栏配置（菜单显示/隐藏、改名、排序）
 *  - 自定义 Head 代码（平台验证标签 / 统计脚本等必须进 <head> 的片段）
 *  - 余额显示配置（付费余额 / 免费额度是否拆分展示）
 *  - 免费/付费分组隔离（免费额度白名单 = 免费分组；白名单外 = 付费分组）
 *  - 号池设置（每日额度、单次上限、自助贡献、自动绑定模型）
 *  - 巡检设置（单模型自动检查与自动下架）
 *
 * 页面骨架必须是 SectionPageLayout：头部（标题 + 保存全部设置）固定，内容区自带
 * overflow-auto 负责滚动；父级 AuthenticatedLayout / Main 都是 overflow-hidden，
 * 页面若自己不带滚动容器，设置项一多就会被直接裁掉。
 *
 * 号池相关几项走 GET/PUT /api/key-pool/settings；导航栏与余额显示各自走 option 接口
 * 独立保存，互不干扰。全部保存即生效，无需重启。
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  getKeyPoolSettings,
  updateKeyPoolSettings,
  type KeyPoolSettings,
} from '@/features/key-pool/api'

import { BalanceDisplayCard } from './balance-display-card'
import { CustomHeadCard } from './custom-head-card'
import { LabCard } from './lab-card'
import { NavCustomCard } from './nav-custom-card'
import { describeSaveFailure } from './save-error'

function IsolationCard(props: {
  settings: KeyPoolSettings
  patch: (next: Partial<KeyPoolSettings>) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Coins className='h-4 w-4' /> 免费 / 付费分组隔离
        </CardTitle>
        <CardDescription>
          免费额度（签到奖励）与免费级 key（号池发放）的可用范围。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-1.5'>
        <Label className='text-sm font-medium'>免费分组白名单</Label>
        <Input
          value={props.settings.free_quota_allowed_groups}
          placeholder='留空表示不限制，例如：号池,公益模型组'
          onChange={(e) =>
            props.patch({ free_quota_allowed_groups: e.target.value })
          }
        />
        <ul className='text-muted-foreground list-disc space-y-1 pl-5 text-xs'>
          <li>列出的分组 = 免费分组：签到等免费额度只在白名单内的分组消耗。</li>
          <li>
            白名单外的分组 = <strong>付费分组</strong>
            ：只扣付费余额，付费不足则请求失败。
          </li>
          <li>
            号池发放的免费级 key
            只能路由到白名单内的分组，调白名单外的分组时自动改绑。
          </li>
          <li>
            填写的分组 key 需与「分组倍率」里配置的分组名一致（逗号分隔）。
          </li>
        </ul>
        <div className='space-y-1.5 pt-2'>
          <Label className='text-sm font-medium'>免费模型白名单</Label>
          <Input
            value={props.settings.free_quota_allowed_models}
            placeholder='留空表示不限制，例如：gpt-4o-mini,claude-3-*'
            onChange={(e) =>
              props.patch({ free_quota_allowed_models: e.target.value })
            }
          />
          <ul className='text-muted-foreground list-disc space-y-1 pl-5 text-xs'>
            <li>列出的模型 = 免费可跑：免费额度只在白名单内的模型上消耗。</li>
            <li>
              支持 <code>prefix*</code> 前缀通配（如 <code>gpt-4o*</code>
              ），也支持写完整模型名。
            </li>
            <li>
              与分组白名单是<strong>同时满足</strong>
              关系：分组在大名单内、模型不在 白名单内，仍然扣付费余额。
            </li>
          </ul>
        </div>
      </CardContent>
    </Card>
  )
}

function KeyPoolCard(props: {
  settings: KeyPoolSettings
  patch: (next: Partial<KeyPoolSettings>) => void
}) {
  const { settings, patch } = props
  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Grid2x2Check className='h-4 w-4' /> 号池设置
        </CardTitle>
        <CardDescription>
          号池发放与运营相关的开关（也可在「号池」页的设置卡片调整）。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='space-y-1.5'>
          <Label className='text-sm font-medium'>用户自助贡献的每日额度</Label>
          <Input
            type='number'
            min={0}
            value={settings.default_daily_gift_quota}
            onChange={(e) =>
              patch({ default_daily_gift_quota: Number(e.target.value) || 0 })
            }
          />
          <p className='text-muted-foreground text-xs'>
            用户贡献密钥后，每个号池 Token 每天赠送的额度；每日 00:00
            （北京时间）自动重置回该值。设为 0 则不赠送。
          </p>
        </div>

        <div className='space-y-1.5'>
          <Label className='text-sm font-medium'>
            用户单次贡献可生成的 Token 数上限
          </Label>
          <Input
            type='number'
            min={1}
            max={100}
            value={settings.user_max_tokens_per_submit}
            onChange={(e) =>
              patch({
                user_max_tokens_per_submit: Number(e.target.value) || 1,
              })
            }
          />
          <p className='text-muted-foreground text-xs'>
            防止用户一次自助签发大量 Token。运营侧生成号池不受此限制。
          </p>
        </div>

        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>开放用户自助贡献</Label>
            <p className='text-muted-foreground text-xs'>
              关闭后用户侧「贡献密钥」入口将不可用，号池只由运营在后台发放。
            </p>
          </div>
          <Switch
            checked={settings.allow_user_contribute}
            onCheckedChange={(v) => patch({ allow_user_contribute: v })}
          />
        </div>

        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>自动绑定上游模型列表</Label>
            <p className='text-muted-foreground text-xs'>
              开启时自动拉取上游 /v1/models 作为渠道可用模型；同时控制巡检前是否
              自动刷新号池渠道的上游全量模型。关闭后模型列表需手填。
            </p>
          </div>
          <Switch
            checked={settings.auto_bind_models}
            onCheckedChange={(v) => patch({ auto_bind_models: v })}
          />
        </div>
      </CardContent>
    </Card>
  )
}

function InspectionCard(props: {
  settings: KeyPoolSettings
  patch: (next: Partial<KeyPoolSettings>) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <ScanSearch className='h-4 w-4' /> 巡检设置
        </CardTitle>
        <CardDescription>
          渠道健康巡检的增强模式。定时周期与并发在
          <Link
            to='/system-settings/request-policies/$section'
            params={{ section: 'health' }}
            className='mx-1 underline underline-offset-2'
          >
            系统设置 → 请求策略 → 渠道健康
          </Link>
          中配置。
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='flex items-start justify-between gap-4 rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label className='text-sm font-medium'>
              单模型自动检查与自动下架
            </Label>
            <p className='text-muted-foreground text-xs'>
              开启后巡检逐个模型探测（号池渠道会先拉取上游全量模型列表再逐个检测）：
              上游明确告知「模型不存在/未授权」时自动下架该模型（不影响渠道和其他模型），
              恢复后自动重新上架。需同时开启渠道健康页的「自动测试渠道」定时任务。
              注意：请求数等于模型数，NVIDIA 号池 85 个模型时每轮巡检约 85
              个请求。
            </p>
          </div>
          <Switch
            checked={props.settings.model_auto_check_enabled}
            onCheckedChange={(v) =>
              props.patch({ model_auto_check_enabled: v })
            }
          />
        </div>
      </CardContent>
    </Card>
  )
}

export function EnhancedSettings() {
  const { t } = useTranslation()
  const [settings, setSettings] = useState<KeyPoolSettings | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    getKeyPoolSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])

  const patch = useCallback((next: Partial<KeyPoolSettings>) => {
    setSettings((prev) => (prev ? { ...prev, ...next } : prev))
  }, [])

  const save = async () => {
    if (!settings) return
    setSaving(true)
    try {
      const saved = await updateKeyPoolSettings(settings)
      setSettings(saved)
      toast.success('扩展设置已保存')
    } catch (error) {
      // 服务端把失败原因放在 message / message_key 里，统一交给 describeSaveFailure
      toast.error(describeSaveFailure(error, '扩展设置保存失败'))
    } finally {
      setSaving(false)
    }
  }

  // 页面必须走 SectionPageLayout：它的 Content 容器自带 overflow-auto。
  // 之前这里直接返回裸 div，而父级 AuthenticatedLayout / Main 都是 overflow-hidden，
  // 结果设置项一多就被裁掉、整页无法上下滚动。
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Extended Settings')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button onClick={save} disabled={saving || !settings}>
          {saving ? t('Saving...') : t('Save all settings')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-4xl space-y-4 pb-4'>
          <p className='text-muted-foreground text-sm'>
            本站新增功能的统一设置入口，保存即生效。
          </p>

          {/* 导航栏配置独立保存（走 option 接口），与下面号池设置的保存按钮互不影响 */}
          <NavCustomCard />

          {/* 自定义 Head 代码：平台验证标签 / 统计脚本这类必须进 <head> 的代码 */}
          <CustomHeadCard />

          {/* 余额显示配置同样独立保存，管理员可决定付费/免费余额怎么展示 */}
          <BalanceDisplayCard />

          {/* 自研实验室展示页（/lab）：纯展示 + 求赞助，与主站业务零关联 */}
          <LabCard />

          {!settings ? (
            <Skeleton className='h-64 w-full' />
          ) : (
            <>
              <IsolationCard settings={settings} patch={patch} />
              <KeyPoolCard settings={settings} patch={patch} />
              <InspectionCard settings={settings} patch={patch} />
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
