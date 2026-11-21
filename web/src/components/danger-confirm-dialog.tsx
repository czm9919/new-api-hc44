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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

type DangerConfirmDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  desc: React.JSX.Element | string
  /** The exact object name the operator must type to unlock the confirm button. */
  objectName: string
  /** Label describing what must be typed, e.g. "channel name". */
  objectLabel?: string
  confirmText?: string
  cancelBtnText?: string
  handleConfirm: () => void
  isLoading?: boolean
  children?: React.ReactNode
  className?: string
}

/**
 * Unified guard for destructive operations (v1.1.0 M1 · B23): the confirm
 * button stays disabled until the operator types the object's exact name,
 * so an accidental click can never trigger an irreversible action.
 */
export function DangerConfirmDialog(props: DangerConfirmDialogProps) {
  const { t } = useTranslation()
  const [typed, setTyped] = useState('')

  useEffect(() => {
    if (props.open) setTyped('')
  }, [props.open])

  const matched = typed.trim() === props.objectName

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.title}
      desc={props.desc}
      confirmText={props.confirmText ?? t('Delete')}
      cancelBtnText={props.cancelBtnText}
      destructive
      disabled={!matched}
      handleConfirm={props.handleConfirm}
      isLoading={props.isLoading}
      className={props.className}
    >
      {props.children}
      <div className='space-y-2'>
        <Label htmlFor='danger-confirm-input'>
          {t('Type {{name}} to confirm', {
            name: props.objectName,
          })}
          {props.objectLabel ? (
            <span className='text-muted-foreground ms-1'>
              ({props.objectLabel})
            </span>
          ) : null}
        </Label>
        <Input
          id='danger-confirm-input'
          value={typed}
          placeholder={props.objectName}
          autoComplete='off'
          onChange={(e) => setTyped(e.target.value)}
        />
        {typed.trim().length > 0 && !matched ? (
          <p className='text-destructive text-xs'>{t('Name does not match')}</p>
        ) : null}
      </div>
    </ConfirmDialog>
  )
}
