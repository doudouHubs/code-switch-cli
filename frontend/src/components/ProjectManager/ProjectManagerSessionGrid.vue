<script setup lang="ts">
import { ref, watch } from 'vue'
import { Check, ChevronDown, ChevronUp } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import ProjectManagerCardMenu from './ProjectManagerCardMenu.vue'
import ProjectManagerCodexStatusLight from './ProjectManagerCodexStatusLight.vue'
import ProjectManagerHighlightedText from './ProjectManagerHighlightedText.vue'
import ProjectManagerMasonryGrid from './ProjectManagerMasonryGrid.vue'
import type {
  CodexSessionRuntimeStatus,
  CodexStatusMonitorInfo,
  SessionSummary,
} from '../../services/projectManager'

type ProjectManagerCardMenuAction = {
  key: string
  label: string
  accent?: boolean
  danger?: boolean
}

const props = defineProps<{
  sessions: SessionSummary[]
  searchKeyword: string
  searchMatches: ReadonlyMap<string, readonly string[]>
  searchNavigationBusy: boolean
  formatUpdatedAt: (timestamp: number) => string
  resolveSummary: (session: SessionSummary) => string
  showProjectNameTag: boolean
  selectionMode: boolean
  isSessionSelected: (sessionId: string) => boolean
  isSessionOpening: (sessionId: string) => boolean
  isSessionDeleting: (sessionId: string) => boolean
  codexMonitor: CodexStatusMonitorInfo
  resolveCodexSessionStatus: (sessionId: string) => CodexSessionRuntimeStatus | undefined
}>()

const emit = defineEmits<{
  rename: [session: SessionSummary]
  delete: [session: SessionSummary]
  'open-session': [session: SessionSummary]
  'open-detail': [session: SessionSummary]
  'toggle-selection': [session: SessionSummary]
}>()

const { t } = useI18n()

const searchMatchIndices = ref(new Map<string, number>())

const resolveSearchMatches = (sessionID: string): readonly string[] =>
  props.searchMatches.get(sessionID) ?? []

const resolveSearchMatchIndex = (sessionID: string) =>
  searchMatchIndices.value.get(sessionID) ?? 0

const resolveCardSummary = (session: SessionSummary) =>
  resolveSearchMatches(session.id)[resolveSearchMatchIndex(session.id)] ?? props.resolveSummary(session)

const canGoToSearchMatch = (sessionID: string, direction: -1 | 1) => {
  // 切换只消费已完成的结果，搜索、批量选择和删除期间不能改变用户正在处理的预览。
  if (props.searchNavigationBusy || props.selectionMode || props.isSessionDeleting(sessionID)) {
    return false
  }
  const nextIndex = resolveSearchMatchIndex(sessionID) + direction
  return nextIndex >= 0 && nextIndex < resolveSearchMatches(sessionID).length
}

const goToSearchMatch = (sessionID: string, direction: -1 | 1) => {
  if (!canGoToSearchMatch(sessionID, direction)) {
    return
  }
  searchMatchIndices.value.set(sessionID, resolveSearchMatchIndex(sessionID) + direction)
}

watch(
  [() => props.searchKeyword, () => props.searchMatches],
  () => {
    // 输入草稿不会传到这里；重新提交成功或清空结果时，所有卡片回到最新一条命中。
    searchMatchIndices.value = new Map()
  },
  { flush: 'sync' },
)

watch(() => props.sessions, (sessions) => {
  const visibleIDs = new Set(sessions.map(session => session.id))
  // 索引跟随会话 ID 而非卡片位置；重排保留当前预览，移除的卡片及时释放状态。
  for (const sessionID of searchMatchIndices.value.keys()) {
    if (!visibleIDs.has(sessionID)) {
      searchMatchIndices.value.delete(sessionID)
    }
  }
})

const resolveSessionActions = (session: SessionSummary): ProjectManagerCardMenuAction[] => [
  {
    key: `rename:${session.id}`,
    label: t('components.projectManager.card.rename'),
  },
  {
    key: `delete:${session.id}`,
    label: t('components.projectManager.card.deleteSession'),
    danger: true,
  },
]

const handleSessionAction = (session: SessionSummary, actionKey: string) => {
  if (props.selectionMode) {
    return
  }
  if (actionKey.startsWith('rename:')) {
    emit('rename', session)
    return
  }
  if (actionKey.startsWith('delete:')) {
    emit('delete', session)
    return
  }
}

const emitOpenSession = (session: SessionSummary) => {
  if (
    props.selectionMode ||
    props.isSessionOpening(session.id) ||
    props.isSessionDeleting(session.id)
  ) {
    return
  }
  emit('open-session', session)
}

const emitOpenDetail = (session: SessionSummary) => {
  if (props.selectionMode || props.isSessionDeleting(session.id)) {
    return
  }
  emit('open-detail', session)
}

const emitToggleSelection = (session: SessionSummary) => {
  if (props.isSessionDeleting(session.id)) {
    return
  }
  emit('toggle-selection', session)
}

const handleSessionCardClick = (session: SessionSummary) => {
  if (props.selectionMode) {
    emitToggleSelection(session)
    return
  }
  emitOpenDetail(session)
}
</script>

<template>
  <ProjectManagerMasonryGrid class="session-grid">
    <article
      v-for="session in sessions"
      :key="session.id"
      :class="[
        'session-card',
        {
          'is-opening': isSessionOpening(session.id),
          'is-deleting': isSessionDeleting(session.id),
          'is-selection-mode': selectionMode,
          'is-selected': selectionMode && isSessionSelected(session.id),
        },
      ]"
      :role="selectionMode ? 'checkbox' : undefined"
      :tabindex="selectionMode ? 0 : undefined"
      :aria-checked="selectionMode ? isSessionSelected(session.id) : undefined"
      :aria-label="selectionMode ? session.display_name : undefined"
      @click="handleSessionCardClick(session)"
      @keydown.enter.self.prevent="emitToggleSelection(session)"
      @keydown.space.self.prevent="emitToggleSelection(session)"
    >
      <span
        v-if="selectionMode"
        class="session-selection-indicator"
        :class="{ 'is-checked': isSessionSelected(session.id) }"
        aria-hidden="true"
      >
        <Check v-if="isSessionSelected(session.id)" :size="14" :stroke-width="2.4" />
      </span>
      <div class="card-topline">
        <div class="card-title-row">
          <ProjectManagerCodexStatusLight
            :monitor="codexMonitor"
            :session-status="resolveCodexSessionStatus(session.id)"
          />
          <h3 class="card-title">
            <ProjectManagerHighlightedText
              :text="session.display_name"
              :keyword="searchKeyword"
            />
          </h3>
        </div>
        <ProjectManagerCardMenu
          :label="t('components.projectManager.card.moreActions')"
          :actions="resolveSessionActions(session)"
          :loading="isSessionDeleting(session.id)"
          :disabled="selectionMode || isSessionDeleting(session.id)"
          @select="handleSessionAction(session, $event)"
        />
      </div>
      <div class="card-copy">
        <p v-if="showProjectNameTag" class="card-eyebrow">
          <ProjectManagerHighlightedText
            :text="session.project_name"
            :keyword="searchKeyword"
          />
        </p>
        <p class="card-summary">
          <ProjectManagerHighlightedText
            :text="resolveCardSummary(session)"
            :keyword="searchKeyword"
          />
        </p>
        <!-- 卡片点击会打开详情，预览切换区域必须阻止冒泡，避免误打开或误选会话。 -->
        <div
          v-if="resolveSearchMatches(session.id).length > 0"
          class="card-search-navigation"
          @click.stop
        >
          <span class="card-search-position" role="status" aria-live="polite" aria-atomic="true">
            {{ t('components.projectManager.card.searchResultStatus', {
              current: resolveSearchMatchIndex(session.id) + 1,
              total: resolveSearchMatches(session.id).length,
            }) }}
          </span>
          <div class="card-search-navigation-actions">
            <button
              type="button"
              :disabled="!canGoToSearchMatch(session.id, -1)"
              :title="t('components.projectManager.card.previousSearchResult')"
              :aria-label="t('components.projectManager.card.previousSearchResult')"
              @click.stop="goToSearchMatch(session.id, -1)"
            >
              <ChevronUp :size="16" aria-hidden="true" />
            </button>
            <button
              type="button"
              :disabled="!canGoToSearchMatch(session.id, 1)"
              :title="t('components.projectManager.card.nextSearchResult')"
              :aria-label="t('components.projectManager.card.nextSearchResult')"
              @click.stop="goToSearchMatch(session.id, 1)"
            >
              <ChevronDown :size="16" aria-hidden="true" />
            </button>
          </div>
        </div>
        <p class="card-path small">
          <ProjectManagerHighlightedText
            :text="showProjectNameTag ? session.project_path : (session.cwd || session.project_path)"
            :keyword="searchKeyword"
          />
        </p>
      </div>
      <div class="card-footer">
        <span class="card-time">{{ formatUpdatedAt(session.updated_at) }}</span>
        <button
          :class="['card-footer-action', { 'is-loading': isSessionOpening(session.id) }]"
          type="button"
          :disabled="selectionMode || isSessionOpening(session.id) || isSessionDeleting(session.id)"
          @click.stop="emitOpenSession(session)"
        >
          <span
            v-if="isSessionOpening(session.id)"
            class="card-footer-spinner"
            aria-hidden="true"
          ></span>
          <span>{{ t('components.projectManager.card.openSession') }}</span>
        </button>
      </div>
    </article>
  </ProjectManagerMasonryGrid>
</template>
