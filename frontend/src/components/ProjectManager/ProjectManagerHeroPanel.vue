<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ListChecks } from '@lucide/vue'
import BaseButton from '../common/BaseButton.vue'
import BaseInput from '../common/BaseInput.vue'
import ProjectManagerGlobalSessionCleanupMenu from './ProjectManagerGlobalSessionCleanupMenu.vue'
import type { SessionDeletionRange } from '../../services/projectManager'
import type { ProjectManagerViewMode } from './types'

type GlobalSessionCleanupRange = Exclude<SessionDeletionRange, 'all'>

const props = defineProps<{
  activeMode: ProjectManagerViewMode
  modelValue: string
  refreshing: boolean
  searching: boolean
  conversationSearch: boolean
  canSelectSessions: boolean
  selectionMode: boolean
  selectionBusy: boolean
  canCleanupSessions: boolean
  cleanupDisabled: boolean
  cleanupBusy: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string]
  'change-mode': [mode: ProjectManagerViewMode]
  search: []
  refresh: []
  clear: []
  'toggle-selection': []
  'cleanup-range': [range: GlobalSessionCleanupRange]
}>()

const { t } = useI18n()

const keywordModel = computed({
  get: () => props.modelValue,
  set: (value: string) => emit('update:modelValue', value),
})

const searchPlaceholder = computed(() => {
  if (props.conversationSearch) {
    return t('components.projectManager.toolbar.conversationSearchPlaceholder')
  }
  if (props.activeMode === 'session') {
    return t('components.projectManager.toolbar.sessionSearchPlaceholder')
  }
  return t('components.projectManager.toolbar.searchPlaceholder')
})

const handleSearchKeydown = (event: KeyboardEvent) => {
  // 输入法确认候选词也会发出 Enter；忽略组合输入和长按重复事件，避免提前或重复搜索。
  if (
    event.isComposing ||
    event.keyCode === 229 ||
    event.repeat ||
    props.selectionBusy
  ) {
    return
  }
  event.preventDefault()
  emit('search')
}
</script>

<template>
  <section class="toolbar">
    <div class="mode-switch tab-group" role="tablist" :aria-label="t('components.projectManager.toolbar.modeLabel')">
      <button
        class="mode-pill tab-pill"
        :class="{ active: activeMode === 'project' }"
        type="button"
        :disabled="selectionBusy"
        @click="emit('change-mode', 'project')"
      >
        {{ t('components.projectManager.toolbar.projectMode') }}
      </button>
      <button
        class="mode-pill tab-pill"
        :class="{ active: activeMode === 'session' }"
        type="button"
        :disabled="selectionBusy"
        @click="emit('change-mode', 'session')"
      >
        {{ t('components.projectManager.toolbar.sessionMode') }}
      </button>
    </div>

    <div class="toolbar-actions">
      <div class="search-shell">
        <span
          v-if="searching"
          class="toolbar-search-spinner"
          role="status"
          :aria-label="t('components.projectManager.toolbar.searchingConversations')"
        ></span>
        <svg v-else viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="11" cy="11" r="7"></circle>
          <path d="M20 20l-3.6-3.6"></path>
        </svg>
        <BaseInput
          v-model="keywordModel"
          class="search-input"
          :disabled="selectionBusy"
          :placeholder="searchPlaceholder"
          @keydown.enter="handleSearchKeydown"
        />
        <button
          v-if="modelValue"
          class="clear-btn"
          type="button"
          :disabled="selectionBusy"
          @click="emit('clear')"
        >
          ×
        </button>
      </div>
      <ProjectManagerGlobalSessionCleanupMenu
        v-if="canCleanupSessions"
        :disabled="cleanupDisabled"
        :busy="cleanupBusy"
        @select-range="emit('cleanup-range', $event)"
      />
      <BaseButton
        v-if="canSelectSessions"
        class="selection-toggle-button"
        :variant="selectionMode ? 'primary' : 'outline'"
        :disabled="selectionBusy"
        :aria-pressed="selectionMode"
        @click="emit('toggle-selection')"
      >
        <ListChecks :size="16" aria-hidden="true" />
        <span>
          {{
            t(
              selectionMode
                ? 'components.projectManager.selection.exit'
                : 'components.projectManager.selection.enter',
            )
          }}
        </span>
      </BaseButton>
      <BaseButton
        variant="outline"
        :disabled="refreshing || selectionBusy"
        :loading="refreshing"
        @click="emit('refresh')"
      >
        {{ t('components.projectManager.toolbar.refresh') }}
      </BaseButton>
    </div>
  </section>
</template>
