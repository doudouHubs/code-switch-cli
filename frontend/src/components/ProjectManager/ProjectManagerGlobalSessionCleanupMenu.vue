<script setup lang="ts">
import { computed } from 'vue'
import { Menu, MenuButton, MenuItem, MenuItems } from '@headlessui/vue'
import { ChevronDown, Clock3, LoaderCircle, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { SessionDeletionRange } from '../../services/projectManager'

type GlobalSessionCleanupRange = Exclude<SessionDeletionRange, 'all'>

const props = withDefaults(
  defineProps<{
    disabled?: boolean
    busy?: boolean
  }>(),
  {
    disabled: false,
    busy: false,
  },
)

const emit = defineEmits<{
  'select-range': [range: GlobalSessionCleanupRange]
}>()

const { t } = useI18n()

const rangeOptions = computed(() => [
  {
    value: 'one_week' as const,
    label: t('components.projectManager.selection.rangeOneWeek'),
  },
  {
    value: 'three_weeks' as const,
    label: t('components.projectManager.selection.rangeThreeWeeks'),
  },
  {
    value: 'one_month' as const,
    label: t('components.projectManager.selection.rangeOneMonth'),
  },
])

const selectRange = (
  range: GlobalSessionCleanupRange,
  close: () => void,
) => {
  // 选择时间范围只提交待确认参数；先收起菜单，避免菜单状态和确认弹窗同时暴露。
  close()
  emit('select-range', range)
}
</script>

<template>
  <Menu as="div" class="global-session-cleanup-menu">
    <MenuButton
      class="btn btn-outline global-session-cleanup-trigger"
      type="button"
      :disabled="props.disabled || props.busy"
      :aria-label="
        t(
          props.busy
            ? 'components.projectManager.globalCleanup.busy'
            : 'components.projectManager.globalCleanup.menuLabel',
        )
      "
      :title="t('components.projectManager.globalCleanup.menuLabel')"
    >
      <LoaderCircle
        v-if="props.busy"
        class="global-session-cleanup-spinner"
        :size="16"
        aria-hidden="true"
      />
      <Trash2 v-else :size="16" aria-hidden="true" />
      <span>{{ t('components.projectManager.globalCleanup.action') }}</span>
      <ChevronDown :size="15" aria-hidden="true" />
    </MenuButton>

    <transition
      enter-active-class="card-menu-enter"
      enter-from-class="card-menu-enter-from"
      enter-to-class="card-menu-enter-to"
      leave-active-class="card-menu-leave"
      leave-from-class="card-menu-leave-from"
      leave-to-class="card-menu-leave-to"
    >
      <MenuItems
        v-if="!props.busy"
        class="card-menu-popover global-session-cleanup-popover"
        :aria-label="t('components.projectManager.globalCleanup.menuLabel')"
      >
        <MenuItem
          v-for="option in rangeOptions"
          :key="option.value"
          v-slot="{ active, close }"
        >
          <button
            :class="['card-menu-item global-session-cleanup-option', { active }]"
            type="button"
            @click="selectRange(option.value, close)"
          >
            <Clock3 :size="16" aria-hidden="true" />
            <span>{{ option.label }}</span>
          </button>
        </MenuItem>
      </MenuItems>
    </transition>
  </Menu>
</template>
