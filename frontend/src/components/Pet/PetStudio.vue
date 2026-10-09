<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import {
  ArrowLeft,
  ArrowRight,
  ImagePlus,
  Layers3,
  Plus,
  RefreshCw,
  Save,
  Settings2,
  Trash2,
  WandSparkles,
  X
} from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { petApi } from './petApi'
import {
  deletePetStudioSkin,
  generatePetStudioImage,
  listPetStudioSkins,
  packPetStudioAtlas,
  processPetStudioFrame,
  readPetStudioAtlas,
  readPetStudioFrameImage,
  savePetStudioSkin,
  splitPetStudioActionSheet,
  toDataUrl,
  type PetStudioActionInput,
  type PetStudioFrameInput,
  type PetStudioSkinRecord
} from './petStudioApi'
import {
  createBlankPetStudioProject,
  createGeneratedPetStudioFrame,
  createImportedPetStudioProject,
  getPetStudioActionIds,
  getPetStudioAtlasFrame,
  getDefaultPetStudioBehaviors,
  isBuiltinPetStudioAction,
  PET_STUDIO_BUILTIN_ACTIONS,
  toPetStudioPackBehaviors,
  type PetStudioActionId,
  type PetStudioAnimation,
  type PetStudioFrame,
  type PetStudioFrameGeometry,
  type PetStudioProject
} from './petStudioModel'
import { createPetStudioSession, type PetStudioReferenceImage } from './petStudioSession'

interface Props { petId?: string }

const props = withDefaults(defineProps<Props>(), { petId: 'default' })
const { t } = useI18n()

type GenerationKind = 'append' | 'replace' | 'replace-frame' | 'batch'
type GenerationStatus = 'idle' | 'running' | 'success' | 'error' | 'cancelled'
type FrameFileMode = 'append' | 'replace'
type SaveMode = 'update' | 'copy'

interface LocalReferenceImage extends PetStudioReferenceImage {}

interface GenerationRequest {
  pose: PetStudioActionId
  subject: string
  prompt: string
  platform: string
  providerId: string
  modelId: string
  frameCount: number
  keyColor: string
  targetHeight: number
  chromaKey: boolean
  references: LocalReferenceImage[]
}

interface GenerationJob {
  kind: GenerationKind
  request: GenerationRequest
  poses: PetStudioActionId[]
  frameId?: string
}

interface PetImageModelReference {
  platform: string
  providerId: string
  modelId: string
}

const studio = createPetStudioSession()
const { state } = studio

const phase = reactive({
  loading: false,
  generating: false,
  processing: false,
  packing: false,
  saving: false,
  deleting: false
})
const generationStatus = ref<GenerationStatus>('idle')
const generationStep = ref<'idle' | 'generate' | 'split' | 'chroma' | 'normalize' | 'pack'>('idle')
const generationToken = ref(0)
const frameSerial = ref(0)
const frameFileMode = ref<FrameFileMode>('append')
const actionIdInput = ref('')
const actionComposerOpen = ref(false)
const behaviorIdInput = ref('')
const saveBind = ref(true)
const sourceChoice = ref(state.sourceSelection)
const skins = ref<PetStudioSkinRecord[]>([])
const errorMessage = ref('')
const notice = ref('')
const generatedPreview = ref('')
const atlasPreview = ref('')
const packedManifest = ref<PetStudioProject['source']['atlas'] extends infer _ ? Record<string, unknown> | null : null>(null)
const packedData = ref('')
const restyleActions = ref<PetStudioActionId[]>([])
const lastGenerationJob = ref<GenerationJob | null>(null)
const framePreviews = reactive<Record<string, string>>({})
const frameFileInput = ref<HTMLInputElement | null>(null)
const referenceFileInput = ref<HTMLInputElement | null>(null)
const projectSettingsOpen = ref(true)
const imageModel = ref<PetImageModelReference | null>(null)
const imageModelLoading = ref(false)
const imageModelError = ref('')
const frameGenerationDialogOpen = ref(false)
const frameGenerationDialogMode = ref<FrameFileMode>('append')
const frameGenerationDialogPrompt = ref('')
const frameGenerationDialogCount = ref(1)

const generation = reactive({
  prompt: '',
  frameCount: 1,
  keyColor: '#00ff00',
  targetHeight: 256,
  chromaKey: true
})

const text = (key: string, params?: Record<string, unknown>, defaultValue?: string): string => {
  const values = { ...(params ?? {}), ...(defaultValue ? { defaultValue } : {}) }
  return t(key, values)
}

const actionLabel = (action: PetStudioActionId): string => text(`pet.studio.actions.${action}`, undefined, action)
const behaviorLabel = (behaviorId: string): string => text(`pet.studio.behaviors.${behaviorId}`, undefined, behaviorId)
const sourceLabel = (source: string): string => text(`pet.studio.source.${source}`, undefined, source)
const stepLabel = (step: string): string => text(`pet.studio.steps.${step}`, undefined, step)

const busy = computed(() => Object.values(phase).some(Boolean) || state.saving || imageModelLoading.value)
const generationRunning = computed(() => generationStatus.value === 'running' && (phase.generating || phase.processing))
const project = computed(() => state.project)
const referenceImages = computed(() => state.referenceImages)
const actionIds = computed<PetStudioActionId[]>(() => {
  const ids = getPetStudioActionIds(project.value.animations)
  // 新工程先展示完整动作骨架，用户可以按动作逐项生成，避免空工程只剩一个 idle 卡片。
  if (project.value.source.kind === 'new') return [...PET_STUDIO_BUILTIN_ACTIONS, ...ids.filter((id) => !isBuiltinPetStudioAction(id))]
  return ids.includes('idle') ? ids : ['idle', ...ids]
})
const existingActionIds = computed(() => getPetStudioActionIds(project.value.animations))
const currentAnimation = computed<PetStudioAnimation | null>(() => project.value.animations[state.selectedPose] ?? null)
const currentFrames = computed(() => currentAnimation.value?.frames ?? [])
const selectedFrame = computed(() => currentFrames.value.find((frame) => frame.id === state.selectedFrameId) ?? currentFrames.value[0] ?? null)
const selectedFrameIndex = computed(() => selectedFrame.value ? currentFrames.value.findIndex((frame) => frame.id === selectedFrame.value?.id) : -1)
const frameGenerationDialogMaxCount = computed(() => frameGenerationDialogMode.value === 'replace'
  ? 1
  : Math.max(1, 8 - currentFrames.value.length))
const frameGenerationDialogTitle = computed(() => frameGenerationDialogMode.value === 'replace' ? '替换动画帧' : '批量新增动画帧')
const frameGenerationDialogSubtitle = computed(() => frameGenerationDialogMode.value === 'replace'
  ? `${actionLabel(state.selectedPose)} · 本次替换 1 帧`
  : `${actionLabel(state.selectedPose)} · 本次最多新增 ${frameGenerationDialogMaxCount.value} 帧`)
const frameGenerationDialogSubmitLabel = computed(() => frameGenerationDialogMode.value === 'replace'
  ? '生成替换帧'
  : `生成 ${Math.min(clampFrameCount(frameGenerationDialogCount.value), frameGenerationDialogMaxCount.value)} 帧`)
const imageModelLabel = computed(() => imageModel.value
  ? `${imageModel.value.platform} · ${imageModel.value.providerId} · ${imageModel.value.modelId}`
  : '未选择图片模型')
const selectedFramePreview = computed(() => selectedFrame.value ? framePreview(selectedFrame.value) : '')
const canPack = computed(() => Boolean(project.value.animations.idle?.frames.length))
const canSave = computed(() => canPack.value && project.value.name.trim().length > 0 && !project.value.source.atlas?.src.includes('file://'))
const canGenerate = computed(() => Boolean(
  (project.value.subject.trim() || referenceImages.value.length || generationReferenceFrames(state.selectedPose).length)
  && imageModel.value?.platform && imageModel.value.providerId && imageModel.value.modelId
))
const canUpdate = computed(() => project.value.source.kind === 'skin' && project.value.source.canUpdate && Boolean(project.value.source.skinId))
const selectedBehavior = computed(() => state.project.behaviors[state.selectedBehaviorId] ?? null)
const behaviorIds = computed(() => Object.keys(project.value.behaviors))
const availableBehaviorActions = computed(() => existingActionIds.value)
const selectedRestyleCount = computed(() => restyleActions.value.filter((action) => Boolean(project.value.animations[action]?.frames.length)).length)

const projectName = computed({
  get: () => project.value.name,
  set: (value: string) => dispatchProject({ type: 'set-name', name: value })
})
const projectSubject = computed({
  get: () => project.value.subject,
  set: (value: string) => dispatchProject({ type: 'set-subject', subject: value })
})

function errorOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function clearMessages(): void {
  errorMessage.value = ''
  notice.value = ''
}

function resetPackedPreview(): void {
  packedData.value = ''
  atlasPreview.value = ''
  packedManifest.value = null
}

function ensureSelection(next: PetStudioProject): void {
  const ids = getPetStudioActionIds(next.animations)
  const pose = ids.includes(state.selectedPose) ? state.selectedPose : ids[0] ?? 'idle'
  const animation = next.animations[pose]
  const selectedId = animation?.frames.some((frame) => frame.id === state.selectedFrameId)
    ? state.selectedFrameId
    : animation?.frames[0]?.id ?? null
  studio.setSession({ selectedPose: pose, selectedFrameId: selectedId })
}

function dispatchProject(action: Parameters<typeof studio.dispatchProject>[0]): PetStudioProject {
  const next = studio.dispatchProject(action)
  resetPackedPreview()
  ensureSelection(next)
  return next
}

function selectPose(pose: PetStudioActionId): void {
  if (busy.value) return
  const animation = project.value.animations[pose]
  studio.setSession({ selectedPose: pose, selectedFrameId: animation?.frames[0]?.id ?? null })
}

function selectFrame(frameId: string): void {
  if (busy.value) return
  studio.setSession({ selectedFrameId: frameId })
}

function imageDataUrl(data: string, mediaType = 'image/png'): string {
  if (!data) return ''
  return toDataUrl(data, mediaType)
}

function nextFrameId(prefix: string): string {
  frameSerial.value += 1
  return `${prefix}:${Date.now().toString(36)}:${frameSerial.value}`
}

function isBuiltinBehavior(behaviorId: string): boolean {
  return Object.prototype.hasOwnProperty.call(getDefaultPetStudioBehaviors(), behaviorId)
}

function isActionReferenced(actionId: string): boolean {
  return Object.values(project.value.behaviors).some((behavior) => behavior.actions.includes(actionId))
}

function framePreview(frame: PetStudioFrame): string {
  if (frame.source.kind === 'file') return frame.source.dataUrl
  return framePreviews[frame.id] || project.value.source.atlas?.src || ''
}

function readFileAsDataUrl(file: File): Promise<{ dataUrl: string; mediaType: string }> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const dataUrl = typeof reader.result === 'string' ? reader.result : ''
      if (!dataUrl) {
        reject(new Error(text('pet.studio.fileReadFailed', undefined, '无法读取图片文件。')))
        return
      }
      resolve({ dataUrl, mediaType: file.type || 'image/png' })
    }
    reader.onerror = () => reject(new Error(text('pet.studio.fileReadFailed', undefined, '无法读取图片文件。')))
    reader.readAsDataURL(file)
  })
}

function readImageGeometry(dataUrl: string, fallbackHeight: number): Promise<PetStudioFrameGeometry> {
  if (typeof Image === 'undefined') {
    const size = Math.max(32, Math.floor(fallbackHeight))
    return Promise.resolve({ width: size, height: size, subjectBounds: { x: 0, y: 0, width: size, height: size } })
  }
  return new Promise((resolve) => {
    const image = new Image()
    image.onload = () => {
      const width = Math.max(1, image.naturalWidth || Math.floor(fallbackHeight))
      const height = Math.max(1, image.naturalHeight || Math.floor(fallbackHeight))
      resolve({ width, height, subjectBounds: { x: 0, y: 0, width, height } })
    }
    image.onerror = () => {
      const size = Math.max(32, Math.floor(fallbackHeight))
      resolve({ width: size, height: size, subjectBounds: { x: 0, y: 0, width: size, height: size } })
    }
    image.src = dataUrl
  })
}

async function cropAtlasFrame(atlasSrc: string, crop: { x: number; y: number; width: number; height: number }): Promise<string> {
  if (typeof document === 'undefined' || typeof Image === 'undefined') return atlasSrc
  const image = new Image()
  const loaded = await new Promise<HTMLImageElement>((resolve, reject) => {
    image.onload = () => resolve(image)
    image.onerror = () => reject(new Error(text('pet.studio.atlasPreviewFailed', undefined, '无法读取 atlas 帧预览。')))
    image.src = atlasSrc
  })
  if (crop.x < 0 || crop.y < 0 || crop.width < 1 || crop.height < 1 || crop.x + crop.width > loaded.naturalWidth || crop.y + crop.height > loaded.naturalHeight) {
    throw new Error(text('pet.studio.atlasCropInvalid', undefined, 'atlas 帧裁剪区域越过图片边界。'))
  }
  const canvas = document.createElement('canvas')
  canvas.width = crop.width
  canvas.height = crop.height
  const context = canvas.getContext('2d')
  if (!context) return atlasSrc
  context.drawImage(loaded, crop.x, crop.y, crop.width, crop.height, 0, 0, crop.width, crop.height)
  return canvas.toDataURL('image/png')
}

async function refreshFramePreviews(): Promise<void> {
  const refreshToken = ++frameSerial.value
  const current = project.value
  const atlasSrc = current.source.atlas?.src
  for (const pose of getPetStudioActionIds(current.animations)) {
    const animation = current.animations[pose]
    if (!animation) continue
    for (const frame of animation.frames) {
      if (frame.source.kind === 'file') {
        framePreviews[frame.id] = frame.source.dataUrl
        continue
      }
      const atlasFrame = getPetStudioAtlasFrame(current, frame.source.pose, frame.source.frameIndex)
      if (!atlasSrc || !atlasFrame) continue
      try {
        const preview = await cropAtlasFrame(atlasSrc, atlasFrame)
        if (refreshToken !== frameSerial.value) return
        framePreviews[frame.id] = preview
      } catch {
        // canvas 读取失败时保留 atlas 本体，保存仍会由 API 再次校验 crop，避免伪造一张空白帧。
        if (refreshToken === frameSerial.value) framePreviews[frame.id] = atlasSrc
      }
    }
  }
}

function clearFramePreviews(): void {
  for (const key of Object.keys(framePreviews)) delete framePreviews[key]
}

async function loadSkins(showError = true): Promise<void> {
  phase.loading = true
  if (showError) errorMessage.value = ''
  try {
    skins.value = await listPetStudioSkins(props.petId)
  } catch (error) {
    if (showError) errorMessage.value = errorOf(error)
  } finally {
    phase.loading = false
  }
}

async function loadImageModel(): Promise<void> {
  imageModelLoading.value = true
  imageModelError.value = ''
  try {
    const settings = await petApi.getSettingsSnapshot(props.petId)
    const dream = settings.dream
    const platform = dream.imageProviderPlatform?.trim() ?? ''
    const providerId = dream.imageProviderId?.trim() ?? ''
    const modelId = dream.imageModelId?.trim() ?? ''
    imageModel.value = platform && providerId && modelId ? { platform, providerId, modelId } : null
  } catch (error) {
    imageModel.value = null
    imageModelError.value = errorOf(error)
  } finally {
    imageModelLoading.value = false
  }
}

function sourceValue(event: Event): string {
  return (event.target as HTMLSelectElement).value
}

function confirmDiscard(): boolean {
  if (!studio.dirty.value) return true
  return typeof window === 'undefined' || window.confirm(text('pet.studio.discardConfirm', undefined, '当前有未保存修改，切换来源会放弃这些修改。继续吗？'))
}

async function readProjectFromSource(selection: string): Promise<PetStudioProject> {
  if (selection === 'new') return createBlankPetStudioProject()
  const result = await readPetStudioAtlas(props.petId, selection === 'default' ? 'default' : { skinId: selection })
  const imported = createImportedPetStudioProject(result.atlas, result.skin)
  // ReadSkin 对默认资源也会返回一条内置记录，但默认资源始终是只读来源，不能被误判为可更新皮肤。
  imported.source = selection === 'default'
    ? { kind: 'default', canUpdate: false, atlas: result.atlas }
    : { kind: 'skin', skinId: selection, canUpdate: imported.builtin !== true, atlas: result.atlas }
  return imported
}

async function changeSource(selection: string): Promise<boolean> {
  if (busy.value || selection === state.sourceSelection) {
    sourceChoice.value = state.sourceSelection
    return false
  }
  if (!confirmDiscard()) {
    sourceChoice.value = state.sourceSelection
    return false
  }
  clearMessages()
  phase.loading = true
  try {
    const next = await readProjectFromSource(selection)
    studio.loadProject(next, selection)
    const firstPose = getPetStudioActionIds(next.animations)[0] ?? 'idle'
    studio.setSession({
      selectedPose: firstPose,
      selectedFrameId: next.animations[firstPose]?.frames[0]?.id ?? null,
      referenceImages: []
    })
    sourceChoice.value = selection
    generatedPreview.value = ''
    lastGenerationJob.value = null
    generationStatus.value = 'idle'
    clearFramePreviews()
    resetPackedPreview()
    await refreshFramePreviews()
    notice.value = text('pet.studio.sourceLoaded', { source: sourceLabel(selection === 'new' ? 'new' : next.source.kind) }, '已加载 {source}。')
    return true
  } catch (error) {
    sourceChoice.value = state.sourceSelection
    errorMessage.value = errorOf(error)
    return false
  } finally {
    phase.loading = false
  }
}

function clampFrameCount(value: number): number {
  return Math.min(8, Math.max(1, Math.floor(Number(value) || 1)))
}

function generationReferenceFrames(pose: PetStudioActionId): PetStudioFrame[] {
  // idle 首帧固定形象，当前帧提供动作上下文；同一帧只发送一次，避免浪费参考图名额。
  const idleFrame = project.value.animations.idle?.frames[0]
  const frames = project.value.animations[pose]?.frames ?? []
  const actionFrame = frames.find((frame) => frame.id === state.selectedFrameId) ?? frames[0]
  const references: PetStudioFrame[] = []
  if (idleFrame) references.push(idleFrame)
  if (actionFrame && actionFrame.id !== idleFrame?.id) references.push(actionFrame)
  return references
}

async function buildGenerationRequest(
  pose: PetStudioActionId,
  frameCount = generation.frameCount,
  prompt = generation.prompt
): Promise<GenerationRequest> {
  const selectedImageModel = imageModel.value
  const referenceProject = project.value
  const referenceFrames = generationReferenceFrames(pose)
  const request: GenerationRequest = {
    pose,
    subject: project.value.subject.trim(),
    prompt: prompt.trim(),
    platform: selectedImageModel?.platform ?? '',
    providerId: selectedImageModel?.providerId ?? '',
    modelId: selectedImageModel?.modelId ?? '',
    frameCount: clampFrameCount(frameCount),
    keyColor: generation.keyColor,
    targetHeight: Math.max(32, Math.floor(Number(generation.targetHeight) || 256)),
    chromaKey: generation.chromaKey,
    references: state.referenceImages.map((reference) => ({ ...reference }))
  }
  // 用户上传的参考图优先；编辑已有形象无需重复填外观文字，直接读取工程中的真实帧。
  if (!request.references.length) {
    request.references = await Promise.all(referenceFrames.map(async (frame) => {
      const image = await readPetStudioFrameImage(frameInputFromProject(referenceProject, pose, frame))
      return { ...image, preview: imageDataUrl(image.data, image.mediaType) }
    }))
  }
  return request
}

async function prepareGenerationRequest(
  pose: PetStudioActionId,
  frameCount = generation.frameCount,
  prompt = generation.prompt
): Promise<GenerationRequest | null> {
  if (busy.value) return null
  clearMessages()
  phase.loading = true
  try {
    // 配置可能在其他设置页被更新，提交时重新读取，不能继续使用进入工坊时的旧模型。
    await loadImageModel()
    if (imageModelError.value) {
      errorMessage.value = imageModelError.value
      return null
    }
    const request = await buildGenerationRequest(pose, frameCount, prompt)
    return validateGenerationRequest(request) ? request : null
  } catch (error) {
    // 参考帧无法读取时立即失败，不能退回无参考图生成，避免把已有宠物形象换掉。
    errorMessage.value = errorOf(error)
    return null
  } finally {
    phase.loading = false
  }
}

function generationPrompt(request: GenerationRequest): string {
  return [
    request.subject,
    request.prompt,
    request.references.length
      ? 'keep the exact same character design, art style, colors and accessories as the reference images; change only the requested pose and action'
      : '',
    request.frameCount > 1
      ? `${request.frameCount}-frame ${request.pose} animation sprite sheet, invisible grid, left to right then top to bottom`
      : `single ${request.pose} pose`,
    request.frameCount > 1
      ? 'same full body pet in every cell, equal cells, no labels or borders, clean background, consistent character design'
      : 'full body pet sprite, centered, clean background, consistent character design'
  ].filter(Boolean).join(', ')
}

function generationActive(token: number): boolean {
  return token === generationToken.value
}

async function createGeneratedFrames(request: GenerationRequest, token: number): Promise<PetStudioFrame[] | null> {
  generationStep.value = 'generate'
  const generated = await generatePetStudioImage(
    props.petId,
    { platform: request.platform, providerId: request.providerId, modelId: request.modelId },
    generationPrompt(request),
    request.references
  )
  if (!generationActive(token)) return null
  generatedPreview.value = imageDataUrl(generated.data, generated.mediaType)

  phase.generating = false
  phase.processing = true
  const sources: Array<{ data: string; mediaType: string }> = request.frameCount > 1
    ? (generationStep.value = 'split', await splitPetStudioActionSheet(generated.data, request.frameCount)).frames.map((frame) => ({ data: frame.data, mediaType: 'image/png' }))
    : [{ data: generated.data, mediaType: generated.mediaType }]
  if (!generationActive(token)) return null

  const result: PetStudioFrame[] = []
  for (const source of sources) {
    if (!generationActive(token)) return null
    generationStep.value = request.chromaKey ? 'chroma' : 'normalize'
    const normalized = await processPetStudioFrame(source.data, {
      chromaKey: request.chromaKey,
      keyColor: request.keyColor,
      targetHeight: request.targetHeight
    })
    if (!generationActive(token)) return null
    generationStep.value = 'normalize'
    const dataUrl = imageDataUrl(normalized)
    result.push(createGeneratedPetStudioFrame({
      id: nextFrameId('generated'),
      dataUrl,
      durationMs: 240,
      geometry: await readImageGeometry(dataUrl, request.targetHeight)
    }))
  }
  return result
}

function applyGeneratedFrames(request: GenerationRequest, frames: PetStudioFrame[], kind: GenerationKind, frameId?: string): void {
  if (kind === 'replace-frame' && frameId && frames[0]) {
    // 单帧 AI 替换必须保留原 frameId，行为引用和当前选中态才能继续指向同一帧。
    dispatchProject({ type: 'replace-frame', pose: request.pose, frameId, frame: { ...frames[0], id: frameId } })
    studio.setSession({ selectedPose: request.pose, selectedFrameId: frameId })
    return
  }
  if (kind === 'replace') {
    const firstId = project.value.animations[request.pose]?.frames[0]?.id ?? frames[0]?.id ?? null
    dispatchProject({ type: 'replace-animation-frames', pose: request.pose, frames })
    studio.setSession({ selectedPose: request.pose, selectedFrameId: firstId })
    return
  }
  const afterId = state.selectedPose === request.pose ? state.selectedFrameId ?? undefined : undefined
  dispatchProject({ type: 'append-frames', pose: request.pose, frames, afterId })
  studio.setSession({ selectedPose: request.pose, selectedFrameId: frames[0]?.id ?? null })
}

async function startGeneration(job: GenerationJob): Promise<void> {
  if (busy.value || !job.poses.length) return
  clearMessages()
  const token = ++generationToken.value
  lastGenerationJob.value = job
  generationStatus.value = 'running'
  generationStep.value = 'generate'
  phase.generating = true
  phase.processing = false
  try {
    for (const pose of job.poses) {
      if (!generationActive(token)) return
      const animation = project.value.animations[pose]
      const request = job.kind === 'batch'
        ? { ...job.request, pose, frameCount: clampFrameCount(animation?.frames.length ?? 1) }
        : { ...job.request, pose }
      const frames = await createGeneratedFrames(request, token)
      if (!frames || !generationActive(token)) return
      if (job.kind === 'replace' && frames.length !== (animation?.frames.length ?? 0)) {
        throw new Error(text('pet.studio.restyleFrameCountMismatch', undefined, '整动作重生成返回的帧数与原动作不一致。'))
      }
      applyGeneratedFrames(request, frames, job.kind === 'batch' ? 'replace' : job.kind, job.frameId)
    }
    if (!generationActive(token)) return
    generationStatus.value = 'success'
    generationStep.value = 'idle'
    notice.value = text('pet.studio.generated', undefined, '生成结果已加入当前草稿。')
  } catch (error) {
    if (!generationActive(token)) return
    generationStatus.value = 'error'
    generationStep.value = 'idle'
    errorMessage.value = errorOf(error)
  } finally {
    if (generationActive(token)) {
      phase.generating = false
      phase.processing = false
    }
  }
}

function validateGenerationRequest(request: GenerationRequest): boolean {
  if (!request.subject && !request.references.length) {
    errorMessage.value = text('pet.studio.validation', undefined, '请先填写宠物外观描述或提供参考图。')
    return false
  }
  if (!request.platform || !request.providerId || !request.modelId) {
    errorMessage.value = text('pet.studio.imageModelRequired', undefined, '请先在宠物设置的“睡眠”中选择图片模型，形象工坊会自动使用当前配置。')
    return false
  }
  return true
}

function openFrameGenerationDialog(mode: FrameFileMode): void {
  if (busy.value) return
  if (mode === 'replace' && !selectedFrame.value) {
    errorMessage.value = text('pet.studio.selectFrameHint', undefined, '请先选择要替换的帧。')
    return
  }
  clearMessages()
  frameGenerationDialogMode.value = mode
  // 弹窗编辑的是当前动作的语义描述；保存到动作元数据后，后续重新生成仍能复用这段描述。
  frameGenerationDialogPrompt.value = currentAnimation.value?.description?.trim() || generation.prompt
  frameGenerationDialogCount.value = mode === 'replace'
    ? 1
    : Math.min(3, Math.max(1, 8 - currentFrames.value.length))
  frameGenerationDialogOpen.value = true
}

function closeFrameGenerationDialog(): void {
  if (busy.value) return
  frameGenerationDialogOpen.value = false
}

function importFrameFromGenerationDialog(): void {
  const mode = frameGenerationDialogMode.value
  closeFrameGenerationDialog()
  openFramePicker(mode)
}

async function submitFrameGeneration(): Promise<void> {
  if (busy.value) return
  const count = Math.min(clampFrameCount(frameGenerationDialogCount.value), frameGenerationDialogMaxCount.value)
  const mode = frameGenerationDialogMode.value
  const frameId = mode === 'replace' ? selectedFrame.value?.id : undefined
  const request = await prepareGenerationRequest(state.selectedPose, count, frameGenerationDialogPrompt.value)
  if (!request) return
  // 校验通过后才写动作元数据；失败仅保留弹窗输入，不让一次无效提交污染草稿。
  generation.prompt = request.prompt
  generation.frameCount = count
  if (currentAnimation.value) setAnimationDescription(request.prompt)
  closeFrameGenerationDialog()
  await startGeneration({
    kind: mode === 'replace' ? 'replace-frame' : 'append',
    request,
    poses: [request.pose],
    frameId
  })
}

async function generate(): Promise<void> {
  const request = await prepareGenerationRequest(state.selectedPose)
  if (!request) return
  await startGeneration({ kind: 'append', request, poses: [request.pose] })
}

async function regenerateAction(): Promise<void> {
  const frames = currentFrames.value
  if (!frames.length) {
    errorMessage.value = text('pet.studio.noFrames', undefined, '当前动作还没有帧。')
    return
  }
  const request = await prepareGenerationRequest(state.selectedPose, frames.length)
  if (!request) return
  await startGeneration({ kind: 'replace', request, poses: [request.pose] })
}

function replaceSelectedFrameWithAI(): void {
  openFrameGenerationDialog('replace')
}

async function batchRestyle(): Promise<void> {
  const poses = restyleActions.value.filter((pose) => Boolean(project.value.animations[pose]?.frames.length))
  if (!poses.length) {
    errorMessage.value = text('pet.studio.selectRestyleActions', undefined, '请先选择至少一个有帧的动作。')
    return
  }
  const request = await prepareGenerationRequest(poses[0], project.value.animations[poses[0]]?.frames.length ?? 1)
  if (!request) return
  await startGeneration({ kind: 'batch', request, poses })
}

function retryGeneration(): void {
  if (!lastGenerationJob.value) return
  void startGeneration(lastGenerationJob.value)
}

function cancelGeneration(): void {
  if (!generationRunning.value) return
  generationToken.value += 1
  phase.generating = false
  phase.processing = false
  generationStatus.value = 'cancelled'
  generationStep.value = 'idle'
  errorMessage.value = ''
  notice.value = text('pet.studio.generationCancelled', undefined, '已取消生成；迟到的 Wails 结果不会写入草稿。')
}

function openFramePicker(mode: FrameFileMode): void {
  if (busy.value) return
  frameFileMode.value = mode
  frameFileInput.value?.click()
}

async function onFrameFileSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || busy.value) return
  clearMessages()
  phase.processing = true
  try {
    const loaded = await readFileAsDataUrl(file)
    const geometry = await readImageGeometry(loaded.dataUrl, generation.targetHeight)
    const frame = createGeneratedPetStudioFrame({ id: nextFrameId('file'), dataUrl: loaded.dataUrl, geometry, durationMs: 240 })
    if (frameFileMode.value === 'replace' && selectedFrame.value) {
      dispatchProject({ type: 'replace-frame', pose: state.selectedPose, frameId: selectedFrame.value.id, frame })
      framePreviews[selectedFrame.value.id] = frame.source.kind === 'file' ? frame.source.dataUrl : loaded.dataUrl
    } else {
      dispatchProject({ type: 'append-frame', pose: state.selectedPose, frame, afterId: state.selectedFrameId ?? undefined })
      framePreviews[frame.id] = loaded.dataUrl
      studio.setSession({ selectedFrameId: frame.id })
    }
    notice.value = text('pet.studio.frameAdded', undefined, '图片帧已加入当前草稿。')
  } catch (error) {
    errorMessage.value = errorOf(error)
  } finally {
    phase.processing = false
  }
}

function openReferencePicker(): void {
  if (busy.value || referenceImages.value.length >= 3) return
  referenceFileInput.value?.click()
}

async function onReferenceFilesSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (!files.length || busy.value) return
  phase.processing = true
  clearMessages()
  try {
    const remaining = 3 - referenceImages.value.length
    const loaded = await Promise.all(files.slice(0, remaining).map(async (file) => {
      const result = await readFileAsDataUrl(file)
      return { data: result.dataUrl, mediaType: result.mediaType, preview: result.dataUrl }
    }))
    studio.setReferenceImages([...state.referenceImages, ...loaded])
    notice.value = text('pet.studio.referenceAdded', { count: referenceImages.value.length }, '已添加 {count} 张参考图。')
  } catch (error) {
    errorMessage.value = errorOf(error)
  } finally {
    phase.processing = false
  }
}

function removeReference(index: number): void {
  if (busy.value) return
  studio.setReferenceImages(state.referenceImages.filter((_, currentIndex) => currentIndex !== index))
}

function setAnimationLabel(value: string): void {
  dispatchProject({ type: 'set-animation-label', pose: state.selectedPose, label: value })
}

function setAnimationDescription(value: string): void {
  dispatchProject({ type: 'set-animation-description', pose: state.selectedPose, description: value })
}

function setAnimationLoop(loop: boolean): void {
  if (!currentAnimation.value) return
  dispatchProject({ type: 'set-animation', pose: state.selectedPose, animation: { ...currentAnimation.value, loop } })
}

function setSelectedDuration(value: number): void {
  if (!selectedFrame.value) return
  dispatchProject({ type: 'set-duration', pose: state.selectedPose, frameId: selectedFrame.value.id, durationMs: Math.floor(Number(value) || 240) })
}

function moveSelectedFrame(direction: -1 | 1): void {
  if (!selectedFrame.value) return
  dispatchProject({ type: 'move-frame', pose: state.selectedPose, frameId: selectedFrame.value.id, direction })
}

function deleteSelectedFrame(): void {
  if (!selectedFrame.value) return
  if (state.selectedPose === 'idle' && currentFrames.value.length === 1) {
    errorMessage.value = text('pet.studio.idleMustKeepFrame', undefined, 'idle 至少需要保留一帧。')
    return
  }
  dispatchProject({ type: 'delete-frame', pose: state.selectedPose, frameId: selectedFrame.value.id })
}

function addAction(): void {
  const actionId = actionIdInput.value.trim()
  if (!/^[a-zA-Z][a-zA-Z0-9_-]*$/.test(actionId) || isBuiltinPetStudioAction(actionId) || actionIds.value.includes(actionId)) {
    errorMessage.value = text('pet.studio.invalidActionId', undefined, '动作 ID 必须以字母开头，且不能与已有或内置动作重复。')
    return
  }
  const seed = selectedFrame.value
  const frame = seed
    ? { ...seed, id: nextFrameId(`action-${actionId}`), source: { ...seed.source } }
    : createGeneratedPetStudioFrame({
        id: nextFrameId(`action-${actionId}`),
        dataUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=',
        geometry: { width: 1, height: 1, subjectBounds: { x: 0, y: 0, width: 1, height: 1 } },
        durationMs: 240
      })
  dispatchProject({ type: 'add-action', actionId, animation: { loop: true, frames: [frame] } })
  actionIdInput.value = ''
  actionComposerOpen.value = false
  selectPose(actionId)
  notice.value = text('pet.studio.actionAdded', undefined, '动作已加入草稿，请继续生成或替换它的帧。')
}

function deleteAction(actionId: PetStudioActionId): void {
  if (isBuiltinPetStudioAction(actionId) || actionId === 'idle') return
  if (isActionReferenced(actionId)) {
    errorMessage.value = text('pet.studio.actionReferenced', undefined, '该动作已被行为引用，先从行为集合中移除后才能删除。')
    return
  }
  if (typeof window !== 'undefined' && !window.confirm(text('pet.studio.deleteActionConfirm', undefined, '删除这个自定义动作？'))) return
  dispatchProject({ type: 'delete-action', actionId })
}

function toggleRestyleAction(actionId: PetStudioActionId, checked: boolean): void {
  if (checked && !restyleActions.value.includes(actionId)) restyleActions.value.push(actionId)
  if (!checked) restyleActions.value = restyleActions.value.filter((value) => value !== actionId)
}

function setBehaviorLabel(value: string): void {
  dispatchProject({ type: 'set-behavior-label', behaviorId: state.selectedBehaviorId, label: value })
}

function toggleBehaviorAction(actionId: PetStudioActionId, checked: boolean): void {
  const behavior = selectedBehavior.value
  if (!behavior) return
  const actions = checked
    ? [...behavior.actions, actionId]
    : behavior.actions.filter((action) => action !== actionId)
  if (!actions.length) {
    errorMessage.value = text('pet.studio.behaviorNeedsAction', undefined, '行为至少需要保留一个动作。')
    return
  }
  dispatchProject({ type: 'set-behavior-actions', behaviorId: state.selectedBehaviorId, actions: [...new Set(actions)] })
}

function moveBehaviorAction(index: number, direction: -1 | 1): void {
  const behavior = selectedBehavior.value
  if (!behavior) return
  const target = index + direction
  if (target < 0 || target >= behavior.actions.length) return
  const actions = [...behavior.actions]
  ;[actions[index], actions[target]] = [actions[target], actions[index]]
  dispatchProject({ type: 'set-behavior-actions', behaviorId: state.selectedBehaviorId, actions })
}

function addBehavior(): void {
  const behaviorId = behaviorIdInput.value.trim()
  const firstAction = existingActionIds.value[0]
  if (!firstAction || !/^[a-zA-Z][a-zA-Z0-9_-]*$/.test(behaviorId) || behaviorIds.value.includes(behaviorId)) {
    errorMessage.value = text('pet.studio.invalidBehaviorId', undefined, '请提供唯一的行为 ID，并先至少生成一个动作。')
    return
  }
  dispatchProject({ type: 'add-behavior', behaviorId, behavior: { actions: [firstAction] } })
  behaviorIdInput.value = ''
  studio.setSession({ selectedBehaviorId: behaviorId })
}

function deleteBehavior(): void {
  if (isBuiltinBehavior(state.selectedBehaviorId)) return
  dispatchProject({ type: 'delete-behavior', behaviorId: state.selectedBehaviorId })
  studio.setSession({ selectedBehaviorId: behaviorIds.value[0] ?? 'feed' })
}

function frameInputFromProject(projectToPack: PetStudioProject, pose: PetStudioActionId, frame: PetStudioFrame): PetStudioFrameInput {
  if (frame.source.kind === 'file') {
    return { data: frame.source.dataUrl, durationMs: frame.durationMs }
  }
  const atlasSrc = projectToPack.source.atlas?.src
  const atlasFrame = getPetStudioAtlasFrame(projectToPack, frame.source.pose, frame.source.frameIndex)
  if (!atlasSrc || !atlasFrame) throw new Error(text('pet.studio.atlasFrameMissing', undefined, '无法读取 atlas 帧数据。'))
  return {
    data: atlasSrc,
    durationMs: frame.durationMs,
    crop: { x: atlasFrame.x, y: atlasFrame.y, width: atlasFrame.width, height: atlasFrame.height }
  }
}

function actionInputsFromProject(projectToPack: PetStudioProject): PetStudioActionInput[] {
  return getPetStudioActionIds(projectToPack.animations).flatMap((pose) => {
    const animation = projectToPack.animations[pose]
    if (!animation?.frames.length) return []
    return [{
      id: pose,
      loop: animation.loop,
      label: animation.label,
      description: animation.description,
      frames: animation.frames.map((frame) => frameInputFromProject(projectToPack, pose, frame))
    }]
  })
}

async function packProject(projectToPack: PetStudioProject): Promise<{ data: string; mediaType: string; manifest: Record<string, unknown> }> {
  if (!projectToPack.animations.idle?.frames.length) {
    throw new Error(text('pet.studio.packFirst', undefined, '请先生成并保留至少一帧 idle。'))
  }
  const metadata = {
    subject: projectToPack.subject.trim(),
    // 生成与保存必须记录同一份当前图片模型，不能再使用旧项目里遗留的手填 modelId。
    modelId: imageModel.value?.modelId ?? projectToPack.modelId.trim(),
    ...(projectToPack.createdAt ? { createdAt: projectToPack.createdAt } : {}),
    ...(projectToPack.updatedAt ? { updatedAt: projectToPack.updatedAt } : {}),
    ...(projectToPack.builtin !== undefined ? { builtin: projectToPack.builtin } : {}),
    ...(projectToPack.assetVersion !== undefined ? { assetVersion: projectToPack.assetVersion } : {}),
    ...(projectToPack.spriteNormalizationVersion !== undefined ? { spriteNormalizationVersion: projectToPack.spriteNormalizationVersion } : {})
  }
  const result = await packPetStudioAtlas(projectToPack.name.trim() || 'Pet', {}, {
    actions: actionInputsFromProject(projectToPack),
    behaviors: toPetStudioPackBehaviors(projectToPack),
    metadata,
    ...metadata
  })
  return { data: result.data, mediaType: result.mediaType, manifest: result.manifest as unknown as Record<string, unknown> }
}

async function packDraft(): Promise<void> {
  if (busy.value || !canPack.value) return
  clearMessages()
  phase.packing = true
  generationStep.value = 'pack'
  try {
    const result = await packProject({ ...project.value, modelId: imageModel.value?.modelId ?? project.value.modelId })
    packedData.value = result.data
    atlasPreview.value = imageDataUrl(result.data, result.mediaType)
    packedManifest.value = result.manifest
    notice.value = text('pet.studio.packed', undefined, 'atlas 已生成，可保存皮肤。')
  } catch (error) {
    errorMessage.value = errorOf(error)
  } finally {
    phase.packing = false
    generationStep.value = 'idle'
  }
}

function safeCopySkinId(name: string): string {
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'skin'
  return `studio-${slug}-${Date.now().toString(36)}`
}

async function saveSkin(mode: SaveMode, bind: boolean): Promise<void> {
  if (busy.value || !canSave.value) return
  const skinId = mode === 'update' ? project.value.source.skinId?.trim() : safeCopySkinId(project.value.name)
  if (!skinId) {
    errorMessage.value = text('pet.studio.updateUnavailable', undefined, '当前来源不支持原地更新，请选择另存副本。')
    return
  }
  clearMessages()
  const revision = studio.beginSave()
  if (revision === null) return
  phase.saving = true
  saveBind.value = bind
  const now = Date.now()
  const saveProjectDraft: PetStudioProject = {
    ...project.value,
    modelId: imageModel.value?.modelId ?? project.value.modelId,
    createdAt: project.value.createdAt ?? now,
    updatedAt: now,
    // 另存副本必须切断内置资源身份，否则新记录会被 UI 当成不可更新的内置皮肤。
    builtin: mode === 'copy' ? false : project.value.builtin
  }
  try {
    const packed = await packProject(saveProjectDraft)
    packedData.value = packed.data
    atlasPreview.value = imageDataUrl(packed.data, 'image/png')
    packedManifest.value = packed.manifest
    await savePetStudioSkin(props.petId, {
      skinId,
      name: saveProjectDraft.name.trim() || 'Pet Studio',
      subject: saveProjectDraft.subject.trim(),
      modelId: saveProjectDraft.modelId.trim(),
      atlas: packed.data,
      manifestJson: packed.manifest,
      bind
    })

    // 保存成功后必须重新 ReadSkin，确保 baseline 使用后端最终 manifest，而不是本地 pack 的猜测。
    const loaded = await readPetStudioAtlas(props.petId, { skinId })
    const imported = createImportedPetStudioProject(loaded.atlas, loaded.skin)
    if (imported.source.kind !== 'skin') {
      imported.source = { kind: 'skin', skinId, canUpdate: true, atlas: loaded.atlas }
    }
    if (studio.commitSavedProject(revision, imported, skinId)) {
      const firstPose = getPetStudioActionIds(imported.animations)[0] ?? 'idle'
      studio.setSession({ selectedPose: firstPose, selectedFrameId: imported.animations[firstPose]?.frames[0]?.id ?? null })
      sourceChoice.value = skinId
      clearFramePreviews()
      resetPackedPreview()
      await refreshFramePreviews()
    }
    await loadSkins(false)
    if (bind) {
      try {
        await petApi.getSnapshot(props.petId)
      } catch (error) {
        // 皮肤已经持久化；运行时刷新失败单独提示，不能把已保存结果说成失败。
        notice.value = text('pet.studio.savedRuntimeRefreshFailed', { error: errorOf(error) }, '皮肤已保存，但运行时刷新失败：{error}')
      }
    }
    if (!notice.value) notice.value = text(bind ? 'pet.studio.savedBound' : 'pet.studio.saved', undefined, bind ? '皮肤已保存并绑定。' : '皮肤已保存。')
  } catch (error) {
    errorMessage.value = errorOf(error)
    notice.value = text('pet.studio.savedButReloadFailed', undefined, '保存请求可能已经到达后端，但重新读取来源失败，请刷新皮肤列表确认。')
    studio.finishSave(revision)
  } finally {
    phase.saving = false
    studio.finishSave(revision)
  }
}

async function removeSkin(skin: PetStudioSkinRecord): Promise<void> {
  if (busy.value || skin.builtin) return
  if (skin.skinId === state.sourceSelection) {
    errorMessage.value = text('pet.studio.deleteCurrentSource', undefined, '当前正在编辑这套皮肤，请先切换来源再删除。')
    return
  }
  if (typeof window !== 'undefined' && !window.confirm(text('pet.studio.deleteConfirm', undefined, '删除这套皮肤？'))) return
  clearMessages()
  phase.deleting = true
  try {
    await deletePetStudioSkin(props.petId, skin.skinId)
    notice.value = text('pet.studio.deleted', undefined, '皮肤已删除。')
    await loadSkins(false)
  } catch (error) {
    errorMessage.value = errorOf(error)
  } finally {
    phase.deleting = false
  }
}

function formatDate(value: number | undefined): string {
  if (!value) return text('pet.studio.notAvailable', undefined, '未设置')
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(value)
}

watch(() => state.selectedPose, (pose) => {
  if (!restyleActions.value.includes(pose) && project.value.animations[pose]?.frames.length) restyleActions.value.push(pose)
})

watch(() => project.value.source.atlas?.src, () => {
  void refreshFramePreviews()
})

onMounted(() => {
  void Promise.all([loadSkins(), loadImageModel()])
})
</script>

<template>
  <section class="pet-studio">
    <header class="studio-header">
      <div class="studio-header__identity">
        <span class="studio-eyebrow">{{ text('pet.studio.eyebrow', undefined, 'PET STUDIO') }}</span>
        <div class="studio-title-row">
          <Layers3 class="studio-title-icon" :size="16" :stroke-width="2" />
          <h2>{{ project.name.trim() || '创建新宠物' }}</h2>
          <span class="studio-status" :class="{ 'is-busy': busy, 'is-dirty': studio.dirty.value }">
            {{ busy ? text('pet.studio.busy', undefined, '处理中') : studio.dirty.value ? text('pet.studio.dirty', undefined, '未保存') : text('pet.studio.ready', undefined, '就绪') }}
          </span>
        </div>
        <p>{{ text('pet.studio.subtitle', undefined, '生成、处理并管理桌宠皮肤。') }}</p>
      </div>
      <nav class="studio-mode-switch studio-mode-switch--header" :aria-label="text('pet.studio.title', undefined, '形象工坊')">
        <button type="button" :class="{ 'is-active': state.editorMode === 'actions' }" :disabled="busy" @click="studio.setSession({ editorMode: 'actions' })"><Layers3 :size="14" :stroke-width="2" />{{ text('pet.studio.actionsTab', undefined, '动作制作') }}</button>
        <button type="button" :class="{ 'is-active': state.editorMode === 'behaviors' }" :disabled="busy" @click="studio.setSession({ editorMode: 'behaviors' })"><Settings2 :size="14" :stroke-width="2" />{{ text('pet.studio.behaviorsTab', undefined, '行为编排') }}</button>
      </nav>
    </header>

    <div v-if="errorMessage" class="studio-message studio-message--error" role="alert">{{ errorMessage }}</div>
    <div v-if="notice" class="studio-message" role="status">{{ notice }}</div>

    <div class="studio-layout" :class="{ 'is-behavior': state.editorMode === 'behaviors' }">
      <section class="studio-editor-pane">
      <aside class="studio-sidebar">
        <div class="sidebar-heading">
          <div>
            <h3>{{ state.editorMode === 'actions' ? 'Actions' : text('pet.studio.behaviorsTitle', undefined, '行为') }}</h3>
          </div>
          <button v-if="state.editorMode === 'actions'" class="studio-subtle-button sidebar-add-trigger" type="button" :disabled="busy" @click="actionComposerOpen = !actionComposerOpen"><Plus :size="14" :stroke-width="2" />{{ text('pet.studio.addAction', undefined, '新增动作') }}</button>
          <span v-else class="count-badge">{{ behaviorIds.length }}</span>
        </div>

        <template v-if="state.editorMode === 'actions'">
        <div class="action-list">
          <div v-for="action in actionIds" :key="action" class="action-row" :class="{ 'is-selected': state.selectedPose === action }">
            <button class="action-row__select" type="button" :disabled="busy" @click="selectPose(action)">
              <span class="action-row__art">
                <img v-if="project.animations[action]?.frames[0]" :src="framePreview(project.animations[action].frames[0])" :alt="actionLabel(action)" />
                <ImagePlus v-else :size="16" :stroke-width="1.7" />
              </span>
              <span class="action-row__copy"><strong>{{ actionLabel(action) }}</strong><small>共 {{ project.animations[action]?.frames.length ?? 0 }} 帧</small><em v-if="!project.animations[action]?.frames.length">这个动作还没有帧。</em><em v-else>已就绪</em></span>
            </button>
            <button v-if="!isBuiltinPetStudioAction(action) && action !== 'idle'" class="action-row__delete" type="button" :disabled="busy" :title="text('pet.common.delete', undefined, '删除')" @click="deleteAction(action)">×</button>
          </div>
        </div>

        <div v-if="actionComposerOpen" class="sidebar-add">
          <input v-model="actionIdInput" :disabled="busy" :placeholder="text('pet.studio.actionIdPlaceholder', undefined, 'custom-action')" @keyup.enter="addAction" />
          <button class="button button--small" type="button" :disabled="busy" @click="addAction">{{ text('pet.studio.addAction', undefined, '新增动作') }}</button>
        </div>
        </template>

        <template v-else>
          <div class="action-list behavior-rail-list">
            <button v-for="behaviorId in behaviorIds" :key="behaviorId" class="behavior-rail-row" type="button" :class="{ 'is-selected': state.selectedBehaviorId === behaviorId }" :disabled="busy" @click="studio.setSession({ selectedBehaviorId: behaviorId })"><span>{{ behaviorLabel(behaviorId) }}</span><small>{{ project.behaviors[behaviorId].actions.length }}</small></button>
          </div>
          <div class="sidebar-add">
            <input v-model="behaviorIdInput" :disabled="busy" :placeholder="text('pet.studio.behaviorIdPlaceholder', undefined, 'morning-routine')" @keyup.enter="addBehavior" />
            <button class="button button--small" type="button" :disabled="busy" @click="addBehavior">{{ text('pet.studio.addBehavior', undefined, '新增行为') }}</button>
          </div>
        </template>

        <div class="sidebar-footnote">
          <span>{{ text('pet.studio.sourceNote', undefined, '只保存 data URL 与受控 skinId，不暴露本地路径。') }}</span>
        </div>
      </aside>

      <section class="studio-editor-content">
      <main class="studio-main" :class="{ 'studio-main--actions': state.editorMode === 'actions' }">
        <nav class="studio-tabs" aria-label="Studio sections">
          <button type="button" :class="{ 'is-active': state.editorMode === 'actions' }" :disabled="busy" @click="studio.setSession({ editorMode: 'actions' })">{{ text('pet.studio.actionsTab', undefined, '动作编辑') }}</button>
          <button type="button" :class="{ 'is-active': state.editorMode === 'behaviors' }" :disabled="busy" @click="studio.setSession({ editorMode: 'behaviors' })">{{ text('pet.studio.behaviorsTab', undefined, '行为编排') }}</button>
        </nav>

        <template v-if="state.editorMode === 'actions'">
          <div class="studio-action-workspace">
            <section class="preview-panel">
              <div class="panel-heading">
                <div>
                  <span class="section-kicker">{{ actionLabel(state.selectedPose) }}</span>
                  <h3>{{ text('pet.studio.previewTitle', undefined, '动作预览') }}</h3>
                </div>
                <span v-if="currentAnimation" class="muted-copy">{{ currentFrames.length }} / 8 {{ text('pet.studio.framesUnit', undefined, '帧') }}</span>
              </div>
              <div class="preview-stage">
                <div class="preview-stage__grid"></div>
                <img v-if="selectedFramePreview" :src="selectedFramePreview" :alt="text('pet.studio.frameAlt', { action: actionLabel(state.selectedPose), index: selectedFrameIndex + 1 }, '动作帧')" />
                <img v-else-if="generatedPreview" :src="generatedPreview" :alt="text('pet.studio.generatedPreviewAlt', undefined, '生成的宠物预览')" />
                <img v-else-if="project.source.atlas?.src" :src="project.source.atlas.src" :alt="text('pet.studio.atlasAlt', undefined, '宠物 atlas 预览')" />
                <div v-else class="preview-stage__empty">
                  <span class="preview-stage__empty-mark">+</span>
                  <strong>{{ text('pet.studio.emptyPreview', undefined, '生成图片后在此预览') }}</strong>
                  <small>{{ text('pet.studio.emptyPreviewHint', undefined, '左侧生成设置会把结果写入当前草稿。') }}</small>
                </div>
                <span v-if="generationRunning" class="preview-stage__loading">{{ stepLabel(generationStep) }}</span>
              </div>
            </section>

            <section v-if="currentFrames.length" class="timeline-panel">
              <div class="panel-heading panel-heading--wrap">
                <div>
                  <span class="section-kicker">{{ text('pet.studio.frameTimeline', undefined, 'FRAME TIMELINE') }}</span>
                  <h3>{{ text('pet.studio.framesTitle', undefined, '帧序列') }}</h3>
                </div>
                <div class="timeline-actions">
                  <button class="studio-subtle-button" type="button" :disabled="busy || currentFrames.length >= 8" @click="openFrameGenerationDialog('append')"><Plus :size="14" :stroke-width="2" />{{ text('pet.studio.appendFrame', undefined, '追加帧') }}</button>
                  <button class="studio-subtle-button" type="button" :disabled="busy || !selectedFrame" @click="openFrameGenerationDialog('replace')"><RefreshCw :size="14" :stroke-width="2" />{{ text('pet.studio.replaceFrame', undefined, '替换当前帧') }}</button>
                </div>
              </div>
              <div v-if="selectedFrame" class="timeline-frame-tools">
                <span>{{ text('pet.studio.selectedFrame', undefined, '选中帧') }} {{ selectedFrameIndex + 1 }} / {{ currentFrames.length }}</span>
                <label><span>{{ text('pet.studio.duration', undefined, '时长') }}</span><input type="number" min="16" max="60000" step="16" :value="selectedFrame.durationMs" :disabled="busy" @change="setSelectedDuration(Number(($event.target as HTMLInputElement).value))" /></label>
                <div class="timeline-frame-tools__actions">
                  <button class="studio-icon-button" type="button" :disabled="busy || selectedFrameIndex <= 0" :title="text('pet.studio.moveLeft', undefined, '向前移动')" :aria-label="text('pet.studio.moveLeft', undefined, '向前移动')" @click="moveSelectedFrame(-1)"><ArrowLeft :size="15" :stroke-width="2" /></button>
                  <button class="studio-icon-button" type="button" :disabled="busy || selectedFrameIndex < 0 || selectedFrameIndex >= currentFrames.length - 1" :title="text('pet.studio.moveRight', undefined, '向后移动')" :aria-label="text('pet.studio.moveRight', undefined, '向后移动')" @click="moveSelectedFrame(1)"><ArrowRight :size="15" :stroke-width="2" /></button>
                  <button class="studio-icon-button studio-icon-button--danger" type="button" :disabled="busy || (state.selectedPose === 'idle' && currentFrames.length === 1)" :title="text('pet.common.delete', undefined, '删除')" :aria-label="text('pet.common.delete', undefined, '删除')" @click="deleteSelectedFrame"><Trash2 :size="15" :stroke-width="2" /></button>
                </div>
              </div>
              <div class="frame-strip">
                <button v-for="(frame, index) in currentFrames" :key="frame.id" class="frame-card" :class="{ 'is-selected': selectedFrame?.id === frame.id }" type="button" :disabled="busy" @click="selectFrame(frame.id)">
                  <span class="frame-card__number">{{ String(index + 1).padStart(2, '0') }}</span>
                  <img :src="framePreview(frame)" :alt="text('pet.studio.frameAlt', { action: actionLabel(state.selectedPose), index: index + 1 }, '动作帧')" />
                  <span class="frame-card__duration">{{ frame.durationMs }}ms</span>
                </button>
                <button v-if="currentFrames.length < 8" class="frame-card frame-card--add" type="button" :disabled="busy" @click="openFrameGenerationDialog('append')"><span>＋</span><small>{{ text('pet.studio.addFrame', undefined, '添加') }}</small></button>
              </div>
              <input ref="frameFileInput" class="visually-hidden" type="file" accept="image/*" :disabled="busy" @change="onFrameFileSelected" />
            </section>
            <aside v-if="state.editorMode === 'actions'" class="studio-action-inspector" aria-label="当前动作操作">
              <header class="studio-action-inspector__header">
                <span class="section-kicker">ACTION</span>
                <div class="studio-action-inspector__title">
                  <strong>{{ actionLabel(state.selectedPose) }}</strong>
                  <span>{{ currentFrames.length }} / 8 {{ text('pet.studio.framesUnit', undefined, '帧') }}</span>
                </div>
                <button v-if="!isBuiltinPetStudioAction(state.selectedPose) && state.selectedPose !== 'idle'" class="studio-icon-button studio-icon-button--danger" type="button" :disabled="busy" :title="text('pet.common.delete', undefined, '删除')" :aria-label="text('pet.common.delete', undefined, '删除')" @click="deleteAction(state.selectedPose)"><Trash2 :size="14" :stroke-width="2" /></button>
              </header>
              <div class="studio-action-inspector__body">
                <button class="studio-action-command studio-action-command--primary" type="button" :disabled="busy || !currentFrames.length" @click="regenerateAction"><RefreshCw :size="14" :stroke-width="2" />{{ text('pet.studio.regenerateAction', undefined, '重新生成此动作') }}</button>
                <label v-if="!isBuiltinPetStudioAction(state.selectedPose)" class="field studio-action-name-field"><span>{{ text('pet.studio.animationLabel', undefined, '动作名称') }}</span><input :value="currentAnimation?.label ?? ''" :disabled="busy || !currentAnimation" :placeholder="actionLabel(state.selectedPose)" @input="setAnimationLabel(($event.target as HTMLInputElement).value)" /></label>
                <div class="studio-action-commands">
                  <button class="studio-action-command" type="button" :disabled="busy || !canGenerate || currentFrames.length >= 8" @click="openFrameGenerationDialog('append')"><Plus :size="14" :stroke-width="2" />AI 批量新增帧</button>
                  <button class="studio-action-command" type="button" :disabled="busy || !canGenerate || !selectedFrame" @click="replaceSelectedFrameWithAI"><WandSparkles :size="14" :stroke-width="2" />AI 替换帧</button>
                </div>
                <p class="studio-action-inspector__hint">预览每帧实时长循环播放。新增帧插入当前帧之后，未选中时追加到末尾。</p>
                <details class="studio-action-details">
                  <summary>{{ text('pet.studio.actionMetadata', undefined, '动作设置') }}</summary>
                  <div class="studio-action-details__body">
                    <label class="field"><span>{{ text('pet.studio.animationDescription', undefined, '动作描述') }}</span><textarea :value="currentAnimation?.description ?? ''" :disabled="busy || !currentAnimation" rows="3" :placeholder="text('pet.studio.animationDescriptionPlaceholder', undefined, '这个动作在什么时候播放？')" @input="setAnimationDescription(($event.target as HTMLTextAreaElement).value)" /></label>
                    <label class="toggle-line"><input type="checkbox" :checked="currentAnimation?.loop !== false" :disabled="busy || !currentAnimation" @change="setAnimationLoop(($event.target as HTMLInputElement).checked)" /><span>{{ text('pet.studio.loop', undefined, '循环播放') }}</span></label>
                  </div>
                </details>
              </div>
            </aside>
          </div>

        </template>

        <section v-else class="behavior-editor">
          <div class="panel-heading panel-heading--wrap"><div><span class="section-kicker">{{ text('pet.studio.behaviorKicker', undefined, 'BEHAVIOR MAP') }}</span><h3>{{ text('pet.studio.behaviorsTitle', undefined, '行为动作集合') }}</h3></div><span class="muted-copy">{{ behaviorIds.length }} {{ text('pet.studio.behaviorsUnit', undefined, '组') }}</span></div>
          <div class="behavior-layout">
            <div v-if="selectedBehavior" class="behavior-detail">
              <div class="section-title-row"><div><h3>{{ behaviorLabel(state.selectedBehaviorId) }}</h3><span class="muted-copy">{{ state.selectedBehaviorId }}</span></div><button v-if="!isBuiltinBehavior(state.selectedBehaviorId)" class="button button--small button--danger" type="button" :disabled="busy" @click="deleteBehavior">{{ text('pet.common.delete', undefined, '删除') }}</button></div>
              <label class="field"><span>{{ text('pet.studio.behaviorLabel', undefined, '行为标签') }}</span><input :value="selectedBehavior.label ?? ''" :disabled="busy" :placeholder="behaviorLabel(state.selectedBehaviorId)" @input="setBehaviorLabel(($event.target as HTMLInputElement).value)" /></label>
              <div class="behavior-actions-block"><span class="field-label">{{ text('pet.studio.behaviorActions', undefined, '动作集合') }}</span><div class="behavior-action-checks"><label v-for="action in availableBehaviorActions" :key="`behavior-${action}`" class="check-chip"><input type="checkbox" :checked="selectedBehavior.actions.includes(action)" :disabled="busy" @change="toggleBehaviorAction(action, ($event.target as HTMLInputElement).checked)" /><span>{{ actionLabel(action) }}</span></label></div></div>
              <div class="behavior-order"><div class="section-title-row"><span class="field-label">{{ text('pet.studio.behaviorOrder', undefined, '播放顺序') }}</span><span class="muted-copy">{{ text('pet.studio.behaviorOrderHint', undefined, '按顺序尝试动作') }}</span></div><div v-for="(action, index) in selectedBehavior.actions" :key="`${state.selectedBehaviorId}-${action}`" class="behavior-order-row"><span class="order-index">{{ index + 1 }}</span><strong>{{ actionLabel(action) }}</strong><span class="behavior-order-spacer"></span><button class="icon-button" type="button" :disabled="busy || index === 0" @click="moveBehaviorAction(index, -1)">↑</button><button class="icon-button" type="button" :disabled="busy || index === selectedBehavior.actions.length - 1" @click="moveBehaviorAction(index, 1)">↓</button></div></div>
            </div>
            <div v-else class="empty-hint">{{ text('pet.studio.behaviorEmpty', undefined, '选择一组行为开始编排。') }}</div>
          </div>
        </section>
      </main>

      <footer class="studio-savebar">
        <div class="studio-savebar__status">
          <span :class="{ 'is-dirty': studio.dirty.value }">{{ studio.dirty.value ? text('pet.studio.dirty', undefined, '有未保存修改') : text('pet.studio.ready', undefined, '已保存') }}</span>
          <span>{{ text('pet.studio.framesTitle', undefined, '帧序列') }} · {{ existingActionIds.length }} {{ text('pet.studio.actionsTitle', undefined, '动作') }}</span>
        </div>
        <div class="studio-savebar__actions">
          <button class="studio-subtle-button" type="button" :disabled="busy || !canPack" @click="packDraft"><ImagePlus :size="14" :stroke-width="2" />{{ text('pet.studio.pack', undefined, '打包 Atlas') }}</button>
          <button v-if="canUpdate" class="studio-subtle-button" type="button" :disabled="busy || !canSave" @click="saveSkin('update', saveBind)"><Save :size="14" :stroke-width="2" />{{ text('pet.studio.saveUpdate', undefined, '更新原形象') }}</button>
          <button class="studio-subtle-button" type="button" :disabled="busy || !canSave" @click="saveSkin('copy', false)">{{ text('pet.studio.saveCopy', undefined, '另存副本') }}</button>
          <button class="studio-command studio-command--primary" type="button" :disabled="busy || !canSave" @click="saveSkin('copy', true)"><Save :size="14" :stroke-width="2" />{{ text('pet.studio.saveAndBind', undefined, '另存并使用') }}</button>
        </div>
      </footer>
      </section>
      </section>

      <aside class="studio-inspector" aria-label="项目设置">
        <header class="studio-inspector__header">
          <div><span class="section-kicker">{{ text('pet.studio.metadataKicker', undefined, 'PROJECT') }}</span><strong><Settings2 :size="13" :stroke-width="2" />项目设置</strong></div>
          <button class="studio-settings-toggle" type="button" :disabled="busy" :aria-expanded="projectSettingsOpen" @click="projectSettingsOpen = !projectSettingsOpen">{{ projectSettingsOpen ? '−' : '+' }}</button>
        </header>
        <div v-if="projectSettingsOpen" class="studio-inspector__body">
        <section class="inspector-section inspector-section--source">
          <div class="inspector-heading"><span class="section-kicker">PROJECT</span><h3>{{ text('pet.studio.sourceLabel', undefined, '编辑来源') }}</h3></div>
          <label class="field"><span>{{ text('pet.studio.sourceLabel', undefined, '来源') }}</span><select :value="sourceChoice" :disabled="busy" @change="changeSource(sourceValue($event))"><option value="new">{{ sourceLabel('new') }}</option><option value="default">{{ sourceLabel('default') }}</option><option v-for="skin in skins" :key="skin.skinId" :value="skin.skinId">{{ skin.name || skin.skinId }}{{ skin.builtin ? ` · ${text('pet.studio.builtin', undefined, '内置')}` : '' }}</option></select></label>
          <p class="studio-settings-hint">{{ project.source.kind === 'new' ? text('pet.studio.newActionHint', undefined, '从描述或参考图开始创建动作。') : canUpdate ? text('pet.studio.updateAvailable', undefined, '当前来源支持原地更新。') : text('pet.studio.copyOnly', undefined, '内置来源只能另存为副本。') }}</p>
          <details class="studio-skin-library">
            <summary>{{ text('pet.studio.savedSkins', undefined, '已保存皮肤') }} · {{ skins.length }}</summary>
            <div class="studio-skin-library__actions"><span>{{ text('pet.studio.sourceLabel', undefined, '来源') }}</span><button class="studio-icon-button" type="button" :disabled="busy" :title="text('pet.common.refresh', undefined, '刷新')" :aria-label="text('pet.common.refresh', undefined, '刷新')" @click="loadSkins()"><RefreshCw :size="14" :stroke-width="2" /></button></div>
            <div v-if="skins.length" class="studio-skin-list"><div v-for="skin in skins" :key="skin.skinId" class="studio-skin-row" :class="{ 'is-selected': skin.skinId === state.sourceSelection }"><button type="button" :disabled="busy" @click="changeSource(skin.skinId)"><strong>{{ skin.name || skin.skinId }}</strong><small>{{ skin.skinId }}</small></button><span v-if="skin.builtin" class="skin-badge">{{ text('pet.studio.builtin', undefined, '内置') }}</span><button v-else class="studio-icon-button studio-icon-button--danger" type="button" :disabled="busy" :title="text('pet.common.delete', undefined, '删除')" :aria-label="text('pet.common.delete', undefined, '删除')" @click="removeSkin(skin)"><Trash2 :size="14" :stroke-width="2" /></button></div></div>
            <p v-else class="studio-settings-hint">{{ text('pet.studio.noSkins', undefined, '暂无自定义皮肤') }}</p>
          </details>
        </section>

        <section class="inspector-section inspector-section--model">
          <div class="inspector-heading"><span class="section-kicker">MODEL</span><h3>{{ text('pet.studio.model', undefined, '模型') }}</h3></div>
          <div class="studio-current-model">
            <span>当前图片模型</span>
            <strong>{{ imageModelLabel }}</strong>
            <button class="studio-icon-button" type="button" :disabled="busy" title="刷新当前模型" aria-label="刷新当前模型" @click="loadImageModel()"><RefreshCw :size="14" :stroke-width="2" /></button>
          </div>
          <p v-if="imageModelError" class="studio-settings-hint studio-settings-hint--error">{{ imageModelError }}</p>
          <p v-else class="studio-settings-hint">模型来自宠物设置“睡眠”中的图片模型下拉框，形象工坊不会要求手动填写 provider。</p>
        </section>

        <section class="inspector-section inspector-section--metadata">
          <div class="inspector-heading"><span class="section-kicker">IDENTITY</span><h3>{{ text('pet.studio.metadata', undefined, '形象信息') }}</h3></div>
          <label class="field"><span>{{ text('pet.studio.name', undefined, '皮肤名称') }}</span><input v-model="projectName" :disabled="busy" :placeholder="text('pet.studio.namePlaceholder', undefined, 'My Pet')" /></label>
          <label class="field"><span>{{ text('pet.studio.subject', undefined, 'Subject') }}</span><textarea v-model="projectSubject" :disabled="busy" rows="2" :placeholder="text('pet.studio.subjectPlaceholder', undefined, '描述宠物外观、材质和识别特征')" /></label>
        </section>

        <section class="inspector-section inspector-section--references">
          <div class="inspector-heading"><span class="section-kicker">REFERENCES</span><h3>{{ text('pet.studio.referenceImages', undefined, '参考图') }} <small>{{ referenceImages.length }}/3</small></h3></div>
          <div class="reference-grid"><div v-for="(reference, index) in referenceImages" :key="`${reference.data}-${index}`" class="reference-thumb"><img :src="reference.preview" :alt="text('pet.studio.referenceAlt', { index: index + 1 }, '参考图')" /><button type="button" :disabled="busy" :title="text('pet.common.delete', undefined, '删除')" @click="removeReference(index)">×</button></div><button v-if="referenceImages.length < 3" class="reference-add" type="button" :disabled="busy" @click="openReferencePicker"><ImagePlus :size="16" :stroke-width="1.8" /><span>{{ text('pet.studio.addReference', undefined, '上传参考图') }}</span></button></div>
          <input ref="referenceFileInput" class="visually-hidden" type="file" accept="image/*" multiple :disabled="busy" @change="onReferenceFilesSelected" />
          <div v-if="project.source.kind === 'new'" class="studio-new-pet-tools">
            <div class="studio-pose-tags">
              <button v-for="action in actionIds" :key="`new-pose-${action}`" type="button" :class="{ 'is-active': state.selectedPose === action }" :disabled="busy" @click="selectPose(action)">{{ actionLabel(action) }}</button>
            </div>
            <button class="button button--primary studio-generate-pet-button" type="button" :disabled="busy || !canGenerate" @click="generate"><WandSparkles :size="14" :stroke-width="2" />{{ text('pet.studio.generate', undefined, '生成宠物') }}</button>
          </div>
        </section>

        <section v-if="project.source.kind !== 'new' && existingActionIds.length" class="inspector-section inspector-section--restyle">
          <div class="inspector-heading"><span class="section-kicker">RESTYLE</span><h3>{{ text('pet.studio.restyleTitle', undefined, '外观重生成') }}</h3></div>
          <div class="restyle-options"><label v-for="action in existingActionIds" :key="`restyle-${action}`" class="check-chip"><input type="checkbox" :checked="restyleActions.includes(action)" :disabled="busy" @change="toggleRestyleAction(action, ($event.target as HTMLInputElement).checked)" /><span>{{ actionLabel(action) }}</span></label></div>
          <button class="studio-subtle-button studio-action-restyle-button" type="button" :disabled="busy || !selectedRestyleCount" @click="batchRestyle"><WandSparkles :size="14" :stroke-width="2" />{{ text('pet.studio.batchRestyle', undefined, '批量 Restyle') }} · {{ selectedRestyleCount }}</button>
        </section>

        <section v-if="project.source.kind !== 'new'" class="inspector-section inspector-section--generate">
          <div class="inspector-heading"><span class="section-kicker">GENERATION</span><h3>{{ text('pet.studio.generate', undefined, '生成宠物') }}</h3></div>
          <p class="studio-settings-hint">{{ actionLabel(state.selectedPose) }} · {{ text('pet.studio.framesUnit', undefined, '帧') }} {{ generation.frameCount }}</p>
          <div class="generation-actions"><button class="button button--primary" type="button" :disabled="busy || !canGenerate" @click="generate"><WandSparkles :size="14" :stroke-width="2" />{{ text('pet.studio.generate', undefined, '生成宠物') }}</button><button v-if="generationRunning" class="button button--danger" type="button" @click="cancelGeneration">{{ text('pet.studio.cancel', undefined, '取消') }}</button><button v-else-if="generationStatus === 'error' || generationStatus === 'cancelled'" class="button button--quiet" type="button" :disabled="busy || !lastGenerationJob" @click="retryGeneration"><RefreshCw :size="14" :stroke-width="2" />{{ text('pet.studio.retry', undefined, '重试') }}</button></div>
          <details class="studio-advanced-settings">
            <summary>{{ text('pet.studio.advanced', undefined, '高级生成参数') }}</summary>
            <div class="studio-advanced-settings__body">
              <label class="field"><span>{{ text('pet.studio.prompt', undefined, '生成补充说明') }}</span><textarea v-model="generation.prompt" :disabled="busy" rows="2" :placeholder="text('pet.studio.promptPlaceholder', undefined, '保持角色外观一致，突出当前动作。')" /></label>
              <div class="field-grid"><label class="field"><span>{{ text('pet.studio.frameCount', undefined, '生成帧数') }}</span><input v-model.number="generation.frameCount" type="number" min="1" max="8" :disabled="busy" /></label><label class="field"><span>{{ text('pet.studio.targetHeight', undefined, '主体高度') }}</span><input v-model.number="generation.targetHeight" type="number" min="32" max="1024" :disabled="busy" /></label></div>
              <div class="color-row"><label class="toggle-line"><input v-model="generation.chromaKey" type="checkbox" :disabled="busy" /><span>{{ text('pet.studio.chromaKey', undefined, '移除色键背景') }}</span></label><label class="color-field" :title="text('pet.studio.keyColor', undefined, 'Key color')"><input v-model="generation.keyColor" type="color" :disabled="busy || !generation.chromaKey" /><span>{{ generation.keyColor }}</span></label></div>
            </div>
          </details>
          <div class="step-tracker"><span v-for="step in ['generate', 'split', 'chroma', 'normalize']" :key="step" :class="{ 'is-active': generationStep === step, 'is-done': generationStatus === 'success' && generationStep === 'idle' }">{{ stepLabel(step) }}</span></div>
        </section>

        <section class="inspector-section inspector-section--meta-foot">
          <dl class="metadata-list"><div><dt>{{ text('pet.studio.sourceKind', undefined, '来源类型') }}</dt><dd>{{ sourceLabel(project.source.kind) }}</dd></div><div><dt>{{ text('pet.studio.createdAt', undefined, '创建时间') }}</dt><dd>{{ formatDate(project.createdAt) }}</dd></div><div><dt>{{ text('pet.studio.updatedAt', undefined, '更新时间') }}</dt><dd>{{ formatDate(project.updatedAt) }}</dd></div></dl>
        </section>

        </div>
      </aside>
    </div>

    <Teleport to="body">
      <Transition name="studio-frame-modal">
        <div v-if="frameGenerationDialogOpen" class="studio-frame-modal-backdrop" role="presentation" @click.self="closeFrameGenerationDialog">
          <section class="studio-frame-modal" role="dialog" aria-modal="true" aria-labelledby="studio-frame-modal-title" tabindex="-1" @keydown.esc="closeFrameGenerationDialog">
            <header class="studio-frame-modal__header">
              <div>
                <h2 id="studio-frame-modal-title">{{ frameGenerationDialogTitle }}</h2>
                <p>{{ frameGenerationDialogSubtitle }}</p>
              </div>
              <button class="studio-frame-modal__close" type="button" :disabled="busy" aria-label="关闭" title="关闭" @click="closeFrameGenerationDialog"><X :size="17" :stroke-width="2" /></button>
            </header>
            <div class="studio-frame-modal__body">
              <label class="studio-frame-modal__field">
                <span>动作描述</span>
                <textarea v-model="frameGenerationDialogPrompt" maxlength="500" rows="3" placeholder="例如：小步向前跑，身体轻微起伏，动作自然循环" :disabled="busy" />
                <small>{{ frameGenerationDialogPrompt.length }}/500</small>
              </label>
              <label class="studio-frame-modal__field">
                <span>{{ frameGenerationDialogMode === 'replace' ? '替换数量' : '新增数量' }}</span>
                <input v-model.number="frameGenerationDialogCount" type="number" min="1" :max="frameGenerationDialogMaxCount" :disabled="busy || frameGenerationDialogMode === 'replace'" />
              </label>
              <p class="studio-frame-modal__hint">预计调用图像模型 1 次。任务串行执行，失败时停止并保留已完成结果。</p>
              <p v-if="errorMessage" class="studio-frame-modal__error" role="alert">{{ errorMessage }}</p>
            </div>
            <footer class="studio-frame-modal__footer">
              <button class="studio-subtle-button" type="button" :disabled="busy" @click="importFrameFromGenerationDialog"><ImagePlus :size="14" :stroke-width="2" />从图片导入</button>
              <span class="studio-frame-modal__footer-spacer"></span>
              <button class="studio-subtle-button" type="button" :disabled="busy" @click="closeFrameGenerationDialog">取消</button>
              <button class="studio-command studio-command--primary" type="button" :disabled="busy" @click="submitFrameGeneration"><Plus :size="14" :stroke-width="2" />{{ frameGenerationDialogSubmitLabel }}</button>
            </footer>
          </section>
        </div>
      </Transition>
    </Teleport>

  </section>
</template>

<style scoped>
.pet-studio { --studio-ink: var(--settings-ink, #1f2933); --studio-muted: var(--settings-muted, #758091); --studio-line: color-mix(in srgb, var(--settings-line, #d8dee6) 82%, transparent); --studio-surface: var(--settings-surface, #ffffff); --studio-soft: color-mix(in srgb, var(--settings-strong-surface, #f4f6f8) 78%, transparent); --studio-accent: var(--mac-accent, #0a84ff); --studio-green: #2d966d; --studio-red: #b84d4d; display: flex; flex-direction: column; gap: 14px; padding: 22px; color: var(--studio-ink); background: linear-gradient(145deg, color-mix(in srgb, var(--studio-soft) 88%, transparent), transparent 42%); }
.pet-studio * { box-sizing: border-box; }
.studio-header, .studio-toolbar, .panel-heading, .section-title-row, .sidebar-heading, .inspector-heading, .studio-header__source, .studio-title-row, .studio-toolbar__actions, .timeline-actions, .generation-actions, .save-actions, .color-row, .toggle-line, .skin-row, .skin-row > div, .packed-preview__heading { display: flex; align-items: center; }
.studio-header, .studio-toolbar, .panel-heading, .section-title-row, .sidebar-heading, .studio-header__source, .studio-title-row, .studio-toolbar__actions, .skin-row, .packed-preview__heading { justify-content: space-between; gap: 12px; }
.studio-header { min-height: 58px; }
.studio-eyebrow, .section-kicker, .source-kicker { display: block; color: var(--studio-accent); font-size: 10px; font-weight: 750; letter-spacing: .14em; line-height: 1.2; text-transform: uppercase; }
.studio-title-row { justify-content: flex-start; margin-top: 4px; }
.studio-header h2, .studio-header p, .panel-heading h3, .sidebar-heading h3, .inspector-heading h3, .section-title-row h3, .behavior-detail h3 { margin: 0; }
.studio-header h2 { font-size: 23px; letter-spacing: -.02em; }
.studio-header p { margin-top: 4px; color: var(--studio-muted); font-size: 12px; }
.studio-status, .count-badge, .skin-badge { border: 1px solid var(--studio-line); border-radius: 999px; color: var(--studio-muted); font-size: 10px; font-weight: 700; letter-spacing: .04em; padding: 4px 8px; white-space: nowrap; }
.studio-status.is-busy { color: #ad762e; }.studio-status.is-dirty { border-color: color-mix(in srgb, #d28a40 45%, var(--studio-line)); color: #ad6725; }
.studio-header__source { min-width: 260px; justify-content: flex-end; }.field--compact { min-width: 210px; }
.studio-message { border: 1px solid color-mix(in srgb, var(--studio-green) 32%, var(--studio-line)); border-radius: 7px; padding: 9px 11px; color: var(--studio-green); font-size: 12px; }.studio-message--error { border-color: color-mix(in srgb, var(--studio-red) 40%, var(--studio-line)); color: var(--studio-red); }
.studio-toolbar { min-height: 46px; border-block: 1px solid var(--studio-line); padding: 8px 0; }.studio-toolbar__source-copy { display: flex; align-items: baseline; flex-wrap: wrap; gap: 8px; }.studio-toolbar__source-copy strong { font-size: 13px; }.source-id, .muted-copy, .frame-source, .save-mode-note, .sidebar-footnote, .empty-hint, .timeline-empty, .metadata-list dd, .skin-row span, .operation-label { color: var(--studio-muted); font-size: 11px; }.source-id { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.studio-toolbar__actions, .timeline-actions, .generation-actions, .save-actions { flex-wrap: wrap; justify-content: flex-end; gap: 7px; }
.studio-layout { display: grid; grid-template-columns: 190px minmax(420px, 1fr) 300px; align-items: start; gap: 14px; }.studio-sidebar, .studio-main, .studio-inspector, .skins-section { min-width: 0; }.studio-sidebar, .studio-inspector { display: flex; flex-direction: column; gap: 12px; }.studio-sidebar { padding-right: 2px; }.studio-main { display: flex; flex-direction: column; gap: 12px; }
.studio-sidebar, .preview-panel, .timeline-panel, .action-detail-grid, .restyle-panel, .behavior-editor, .inspector-section, .skins-section { border: 1px solid var(--studio-line); background: color-mix(in srgb, var(--studio-surface) 90%, transparent); box-shadow: 0 8px 30px color-mix(in srgb, #243042 7%, transparent); }
.studio-sidebar { border: 1px solid var(--studio-line); border-radius: 9px; padding: 12px 10px; background: color-mix(in srgb, var(--studio-soft) 82%, transparent); }.preview-panel, .timeline-panel, .restyle-panel, .behavior-editor, .inspector-section, .skins-section { border-radius: 9px; padding: 14px; }.action-detail-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1px; overflow: hidden; border-radius: 9px; background: var(--studio-line); }.detail-section { min-width: 0; padding: 14px; background: var(--studio-surface); }
.panel-heading, .sidebar-heading, .inspector-heading { min-height: 34px; }.panel-heading--wrap { flex-wrap: wrap; }.panel-heading h3, .sidebar-heading h3, .inspector-heading h3, .section-title-row h3 { margin-top: 3px; font-size: 14px; letter-spacing: -.01em; }.inspector-heading h3 small { color: var(--studio-muted); font-size: 10px; font-weight: 600; }
.studio-tabs { display: flex; border-bottom: 1px solid var(--studio-line); gap: 18px; }.studio-tabs button { position: relative; border: 0; padding: 7px 0 10px; background: transparent; color: var(--studio-muted); cursor: pointer; font: inherit; font-size: 12px; font-weight: 700; }.studio-tabs button.is-active { color: var(--studio-ink); }.studio-tabs button.is-active::after { position: absolute; right: 0; bottom: -1px; left: 0; height: 2px; background: var(--studio-accent); content: ''; }
.action-list { display: flex; flex-direction: column; gap: 3px; margin-top: 9px; }.action-row { display: flex; align-items: center; min-height: 34px; border: 1px solid transparent; border-radius: 6px; }.action-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 28%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 8%, transparent); }.action-row__select { display: flex; flex: 1; align-items: center; min-width: 0; border: 0; padding: 7px 6px; background: transparent; color: inherit; cursor: pointer; font: inherit; text-align: left; }.action-row__dot { width: 6px; height: 6px; flex: 0 0 6px; margin-right: 8px; border-radius: 50%; background: var(--studio-green); }.action-row__dot.is-empty { background: var(--studio-line); }.action-row__name { overflow: hidden; flex: 1; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }.action-row__count { color: var(--studio-muted); font-size: 10px; }.action-row__delete { width: 25px; border: 0; background: transparent; color: var(--studio-muted); cursor: pointer; font-size: 16px; }.action-row__delete:hover { color: var(--studio-red); }
.sidebar-add { display: flex; flex-direction: column; gap: 6px; margin-top: 10px; }.sidebar-add input, .field input, .field textarea, .field select, .field--compact select { width: 100%; border: 1px solid var(--studio-line); border-radius: 5px; outline: none; background: var(--studio-surface); color: var(--studio-ink); font: inherit; font-size: 11px; }.sidebar-add input, .field input, .field textarea { padding: 8px 9px; }.field select, .field--compact select { padding: 7px 8px; }.field textarea { min-height: 58px; resize: vertical; }.sidebar-add input:focus, .field input:focus, .field textarea:focus, .field select:focus { border-color: color-mix(in srgb, var(--studio-accent) 60%, var(--studio-line)); }.sidebar-footnote { margin-top: auto; border-top: 1px solid var(--studio-line); padding-top: 10px; line-height: 1.5; }
.button, .icon-button { border: 1px solid transparent; border-radius: 5px; cursor: pointer; font: inherit; font-size: 11px; font-weight: 700; }.button { min-height: 30px; padding: 7px 10px; background: var(--studio-accent); color: #fff; }.button--small { min-height: 27px; padding: 5px 8px; font-size: 10px; }.button--primary { background: var(--studio-green); }.button--quiet { border-color: var(--studio-line); background: transparent; color: var(--studio-ink); }.button--danger { border-color: color-mix(in srgb, var(--studio-red) 32%, var(--studio-line)); background: color-mix(in srgb, var(--studio-red) 8%, transparent); color: var(--studio-red); }.icon-button { display: inline-grid; width: 29px; height: 29px; place-items: center; border-color: var(--studio-line); background: transparent; color: var(--studio-ink); }.icon-button--danger { color: var(--studio-red); }.button:disabled, .icon-button:disabled, .studio-tabs button:disabled, .action-row button:disabled, .behavior-list button:disabled, .reference-add:disabled { cursor: not-allowed; opacity: .42; }
.preview-stage { position: relative; display: grid; min-height: 285px; margin-top: 11px; place-items: center; overflow: hidden; border: 1px solid var(--studio-line); border-radius: 7px; background: #edf0ee; background-image: linear-gradient(45deg, #dfe5e1 25%, transparent 25%), linear-gradient(-45deg, #dfe5e1 25%, transparent 25%), linear-gradient(45deg, transparent 75%, #dfe5e1 75%), linear-gradient(-45deg, transparent 75%, #dfe5e1 75%); background-position: 0 0, 0 10px, 10px -10px, -10px 0; background-size: 20px 20px; }.preview-stage__grid { position: absolute; inset: 0; opacity: .25; background: linear-gradient(90deg, transparent 49.8%, #fff 50%, transparent 50.2%), linear-gradient(0deg, transparent 49.8%, #fff 50%, transparent 50.2%); background-size: 50% 50%; pointer-events: none; }.preview-stage img { position: relative; z-index: 1; max-width: 92%; max-height: 270px; object-fit: contain; }.preview-stage__empty { position: relative; z-index: 1; display: flex; flex-direction: column; align-items: center; gap: 6px; color: var(--studio-muted); text-align: center; }.preview-stage__empty-mark { display: grid; width: 38px; height: 38px; place-items: center; border: 1px dashed var(--studio-muted); border-radius: 50%; font-size: 21px; font-weight: 300; }.preview-stage__empty strong { font-size: 12px; }.preview-stage__empty small { font-size: 10px; }.preview-stage__loading { position: absolute; right: 10px; bottom: 9px; z-index: 2; border: 1px solid color-mix(in srgb, var(--studio-accent) 28%, var(--studio-line)); border-radius: 999px; padding: 5px 8px; background: color-mix(in srgb, var(--studio-surface) 86%, transparent); color: var(--studio-accent); font-size: 10px; }
.frame-strip { display: flex; gap: 8px; overflow-x: auto; margin-top: 11px; padding: 2px 1px 5px; }.frame-card { position: relative; display: flex; width: 78px; height: 94px; flex: 0 0 78px; flex-direction: column; align-items: stretch; overflow: hidden; border: 1px solid var(--studio-line); border-radius: 6px; background: var(--studio-surface); color: var(--studio-ink); cursor: pointer; font: inherit; }.frame-card.is-selected { border-color: var(--studio-accent); box-shadow: 0 0 0 2px color-mix(in srgb, var(--studio-accent) 16%, transparent); }.frame-card img { width: 100%; height: 68px; object-fit: contain; background: #eef1ef; background-image: linear-gradient(45deg, #dfe5e1 25%, transparent 25%), linear-gradient(-45deg, #dfe5e1 25%, transparent 25%), linear-gradient(45deg, transparent 75%, #dfe5e1 75%), linear-gradient(-45deg, transparent 75%, #dfe5e1 75%); background-position: 0 0, 0 7px, 7px -7px, -7px 0; background-size: 14px 14px; }.frame-card__number { position: absolute; top: 4px; left: 4px; z-index: 1; border-radius: 3px; padding: 2px 4px; background: color-mix(in srgb, #111 66%, transparent); color: #fff; font-size: 9px; }.frame-card__duration { overflow: hidden; padding: 5px; color: var(--studio-muted); font-size: 9px; text-overflow: ellipsis; white-space: nowrap; }.frame-card--add { align-items: center; justify-content: center; gap: 5px; border-style: dashed; color: var(--studio-muted); }.frame-card--add span { font-size: 22px; font-weight: 300; }.frame-card--add small { font-size: 10px; }.timeline-empty { margin-top: 11px; border: 1px dashed var(--studio-line); border-radius: 6px; padding: 21px; text-align: center; }
.field { display: flex; flex-direction: column; gap: 5px; min-width: 0; color: var(--studio-muted); font-size: 10px; font-weight: 700; }.field-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }.toggle-line { justify-content: flex-start; gap: 7px; min-width: 0; color: var(--studio-muted); cursor: pointer; font-size: 10px; }.toggle-line input, .check-chip input { accent-color: var(--studio-accent); }.color-row { justify-content: space-between; gap: 8px; }.color-field { display: flex; align-items: center; gap: 6px; color: var(--studio-muted); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 10px; }.color-field input { width: 25px; height: 22px; padding: 1px; border: 1px solid var(--studio-line); border-radius: 4px; background: transparent; }.generation-actions { margin-top: 2px; }.generation-actions .button--primary { flex: 1; }.step-tracker { display: flex; gap: 4px; margin-top: 2px; }.step-tracker span { border-radius: 3px; padding: 3px 5px; background: var(--studio-soft); color: var(--studio-muted); font-size: 9px; }.step-tracker span.is-active { background: color-mix(in srgb, var(--studio-accent) 14%, transparent); color: var(--studio-accent); }.step-tracker span.is-done { color: var(--studio-green); }
.action-detail-grid .detail-section { display: flex; flex-direction: column; gap: 10px; }.selected-frame-controls { display: grid; grid-template-columns: 76px 1fr; gap: 10px; }.selected-frame-preview { display: grid; min-height: 76px; place-items: center; overflow: hidden; border: 1px solid var(--studio-line); border-radius: 5px; background: #eef1ef; background-image: linear-gradient(45deg, #dfe5e1 25%, transparent 25%), linear-gradient(-45deg, #dfe5e1 25%, transparent 25%), linear-gradient(45deg, transparent 75%, #dfe5e1 75%), linear-gradient(-45deg, transparent 75%, #dfe5e1 75%); background-position: 0 0, 0 7px, 7px -7px, -7px 0; background-size: 14px 14px; }.selected-frame-preview img { max-width: 100%; max-height: 100%; object-fit: contain; }.selected-frame-fields { display: flex; flex-direction: column; gap: 8px; }.frame-move-actions { display: flex; gap: 5px; }.frame-source { display: block; line-height: 1.4; }
.restyle-panel { display: flex; flex-direction: column; gap: 9px; }.restyle-options, .behavior-action-checks { display: flex; flex-wrap: wrap; gap: 6px; }.check-chip { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--studio-line); border-radius: 999px; padding: 5px 7px; color: var(--studio-muted); cursor: pointer; font-size: 10px; }.check-chip:has(input:checked) { border-color: color-mix(in srgb, var(--studio-accent) 42%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 9%, transparent); color: var(--studio-ink); }
.behavior-editor { min-height: 400px; }.behavior-layout { display: grid; grid-template-columns: 145px minmax(0, 1fr); gap: 16px; margin-top: 13px; }.behavior-list { display: flex; flex-direction: column; gap: 3px; }.behavior-list > button { display: flex; align-items: center; justify-content: space-between; border: 1px solid transparent; border-radius: 5px; padding: 8px; background: transparent; color: var(--studio-ink); cursor: pointer; font: inherit; font-size: 11px; text-align: left; }.behavior-list > button.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 28%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 8%, transparent); }.behavior-list small { color: var(--studio-muted); }.behavior-add { margin-top: 8px; }.behavior-detail { display: flex; flex-direction: column; gap: 14px; }.behavior-actions-block, .behavior-order { display: flex; flex-direction: column; gap: 8px; }.field-label { color: var(--studio-muted); font-size: 10px; font-weight: 700; }.behavior-order { border-top: 1px solid var(--studio-line); padding-top: 12px; }.behavior-order-row { display: flex; align-items: center; gap: 8px; border-bottom: 1px solid var(--studio-line); padding: 7px 0; }.order-index { display: grid; width: 21px; height: 21px; place-items: center; border-radius: 4px; background: var(--studio-soft); color: var(--studio-muted); font-size: 10px; }.behavior-order-row strong { font-size: 11px; }.behavior-order-spacer { flex: 1; }
.reference-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px; }.reference-thumb, .reference-add { position: relative; display: grid; min-height: 56px; place-items: center; overflow: hidden; border: 1px dashed var(--studio-line); border-radius: 5px; background: var(--studio-soft); }.reference-thumb img { width: 100%; height: 68px; object-fit: cover; }.reference-thumb button { position: absolute; top: 2px; right: 2px; width: 18px; height: 18px; border: 0; border-radius: 50%; background: #111a; color: #fff; cursor: pointer; line-height: 15px; }.reference-add { min-height: 68px; border-style: dashed; background: transparent; color: var(--studio-muted); cursor: pointer; }.reference-add > span { font-size: 9px; }
.inspector-section { display: flex; flex-direction: column; gap: 10px; }.inspector-heading { align-items: flex-start; flex-direction: column; gap: 0; }.metadata-list { display: flex; flex-direction: column; gap: 6px; margin: 2px 0 0; }.metadata-list div { display: flex; justify-content: space-between; gap: 8px; border-top: 1px solid var(--studio-line); padding-top: 6px; }.metadata-list dt { color: var(--studio-muted); font-size: 10px; }.metadata-list dd { margin: 0; overflow: hidden; text-align: right; text-overflow: ellipsis; white-space: nowrap; }.inspector-section--save { border-color: color-mix(in srgb, var(--studio-green) 30%, var(--studio-line)); }.save-mode-note { line-height: 1.45; }.save-actions { display: grid; grid-template-columns: 1fr 1fr; }.save-actions .button--primary { grid-column: 1 / -1; }.packed-preview { border-top: 1px solid var(--studio-line); padding-top: 10px; }.packed-preview__heading span { color: var(--studio-muted); font-size: 10px; font-weight: 700; }.packed-preview__heading small { color: var(--studio-green); font-size: 9px; }.packed-preview img { display: block; width: 100%; max-height: 100px; margin-top: 7px; object-fit: contain; background: #eef1ef; }
.skins-section { display: flex; flex-direction: column; gap: 10px; }.skin-list { display: flex; flex-direction: column; gap: 6px; }.skin-row { border-top: 1px solid var(--studio-line); padding-top: 8px; }.skin-row > div { min-width: 0; flex: 1; align-items: baseline; justify-content: flex-start; gap: 8px; }.skin-row strong { overflow: hidden; font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }.skin-row span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.skin-badge { color: var(--studio-accent); }
.visually-hidden { position: absolute !important; width: 1px !important; height: 1px !important; overflow: hidden !important; clip: rect(1px, 1px, 1px, 1px) !important; white-space: nowrap !important; }
@media (max-width: 1120px) { .studio-layout { grid-template-columns: 170px minmax(380px, 1fr); }.studio-inspector { grid-column: 1 / -1; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; }.inspector-section--save { grid-column: 1 / -1; } }
@media (max-width: 760px) { .pet-studio { padding: 14px 10px; }.studio-header { align-items: flex-start; flex-direction: column; }.studio-header__source { width: 100%; justify-content: flex-start; }.studio-layout { grid-template-columns: 1fr; }.studio-sidebar { order: 0; }.studio-main { order: 1; }.studio-inspector { order: 2; display: flex; }.action-detail-grid { grid-template-columns: 1fr; }.behavior-layout { grid-template-columns: 1fr; }.studio-toolbar { align-items: flex-start; flex-direction: column; }.studio-toolbar__actions { justify-content: flex-start; }.preview-stage { min-height: 230px; }.save-actions { grid-template-columns: 1fr; }.save-actions .button--primary { grid-column: auto; } }
/* 工作台布局重排：保留所有现有 DOM 和交互，仅重新分配视觉层级与响应式空间。 */
.pet-studio {
  --studio-shadow: 0 18px 42px color-mix(in srgb, #26354a 8%, transparent);
  --studio-shadow-soft: 0 8px 22px color-mix(in srgb, #26354a 5%, transparent);
  box-sizing: border-box;
  width: 100%;
  gap: 16px;
  max-width: 1440px;
  margin: 0 auto;
  padding: 26px clamp(18px, 2.6vw, 34px) 34px;
  background:
    radial-gradient(circle at 88% 4%, color-mix(in srgb, var(--studio-accent) 8%, transparent), transparent 24rem),
    radial-gradient(circle at 7% 12%, color-mix(in srgb, var(--studio-green) 5%, transparent), transparent 20rem),
    linear-gradient(150deg, color-mix(in srgb, var(--studio-soft) 88%, transparent), transparent 48%);
}

.studio-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 26px;
  min-height: 0;
  padding: 2px 0 18px;
  border-bottom: 1px solid var(--studio-line);
}

.studio-header__identity { min-width: 0; }
.studio-eyebrow, .section-kicker, .source-kicker {
  letter-spacing: .14em;
  font-size: 10px;
  font-weight: 800;
}
.studio-eyebrow { color: var(--studio-accent); }
.studio-title-row { justify-content: flex-start; gap: 10px; margin-top: 4px; }
.studio-title-row h2 {
  margin: 0;
  color: var(--studio-ink);
  font-size: clamp(25px, 2.3vw, 32px);
  line-height: 1.05;
  letter-spacing: -.055em;
}
.studio-header__identity > p {
  max-width: 38rem;
  margin: 8px 0 0;
  color: var(--studio-muted);
  font-size: 12px;
  line-height: 1.55;
}
.studio-status {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  border: 1px solid color-mix(in srgb, var(--studio-green) 25%, var(--studio-line));
  border-radius: 999px;
  padding: 0 9px;
  background: color-mix(in srgb, var(--studio-green) 9%, transparent);
  color: var(--studio-green);
  font-size: 10px;
  font-weight: 700;
}
.studio-status.is-busy { border-color: color-mix(in srgb, var(--studio-accent) 32%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 10%, transparent); color: var(--studio-accent); }
.studio-status.is-dirty { border-color: color-mix(in srgb, #ce8a35 34%, var(--studio-line)); background: color-mix(in srgb, #ce8a35 9%, transparent); color: #ae6d20; }

.studio-header__source {
  align-items: flex-end;
  gap: 8px;
  padding: 10px;
  border: 1px solid var(--studio-line);
  border-radius: 12px;
  background: color-mix(in srgb, var(--studio-surface) 80%, transparent);
  box-shadow: var(--studio-shadow-soft);
}
.field--compact { min-width: 178px; }
.field--compact select { min-height: 34px; }

.studio-message {
  margin: 0;
  border: 1px solid color-mix(in srgb, var(--studio-green) 26%, var(--studio-line));
  border-radius: 10px;
  padding: 10px 13px;
  background: color-mix(in srgb, var(--studio-green) 7%, var(--studio-surface));
  color: var(--studio-green);
  font-size: 12px;
  line-height: 1.45;
}
.studio-message--error { border-color: color-mix(in srgb, var(--studio-red) 34%, var(--studio-line)); background: color-mix(in srgb, var(--studio-red) 8%, var(--studio-surface)); color: var(--studio-red); }

.studio-toolbar {
  min-height: 48px;
  padding: 8px 10px 8px 14px;
  border: 1px solid var(--studio-line);
  border-left: 3px solid color-mix(in srgb, var(--studio-accent) 78%, var(--studio-line));
  border-radius: 11px;
  background: color-mix(in srgb, var(--studio-surface) 74%, transparent);
  box-shadow: var(--studio-shadow-soft);
}
.studio-toolbar__source-copy { min-width: 0; }
.studio-toolbar__actions {
  flex: 0 0 auto;
  flex-wrap: wrap;
}
.pet-studio .button,
.pet-studio .studio-tabs button {
  /* 中文词组和英文资源名必须作为一个按钮标签展示；空间不足时换按钮行，不能拆字。 */
  flex-shrink: 0;
  white-space: nowrap;
}
.studio-toolbar__source-copy strong {
  overflow: hidden;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.source-kicker { color: var(--studio-accent); }
.source-id {
  max-width: min(36vw, 300px);
  overflow: hidden;
  border-left: 1px solid var(--studio-line);
  padding-left: 9px;
  color: var(--studio-muted);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.studio-layout {
  grid-template-columns: minmax(170px, 190px) minmax(0, 1fr) minmax(268px, 296px);
  align-items: start;
  gap: 18px;
}
.studio-sidebar, .studio-main, .studio-inspector, .skins-section { min-width: 0; }
.studio-main { gap: 16px; }
.studio-sidebar, .studio-inspector { gap: 14px; }

.studio-sidebar {
  position: sticky;
  top: 16px;
  padding: 15px 11px 12px;
  border-radius: 14px;
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--studio-soft) 86%, var(--studio-surface)), color-mix(in srgb, var(--studio-surface) 78%, transparent));
  box-shadow: var(--studio-shadow-soft);
}
.sidebar-heading { align-items: flex-start; gap: 8px; padding: 0 3px 9px; }
.sidebar-heading h3, .panel-heading h3, .inspector-heading h3, .section-title-row h3 {
  margin: 3px 0 0;
  color: var(--studio-ink);
  font-size: 14px;
  letter-spacing: -.02em;
}
.count-badge {
  display: grid;
  width: 28px;
  height: 28px;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--studio-accent) 22%, var(--studio-line));
  border-radius: 50%;
  background: color-mix(in srgb, var(--studio-accent) 8%, transparent);
  color: var(--studio-accent);
  font-size: 10px;
  font-weight: 800;
}
.action-list {
  max-height: min(56vh, 540px);
  overflow: auto;
  padding: 2px;
}
.action-row { min-height: 34px; border-radius: 7px; }
.action-row + .action-row { margin-top: 2px; }
.action-row__select { border-radius: 7px; padding: 8px 7px; }
.action-row.is-selected .action-row__select {
  background: color-mix(in srgb, var(--studio-accent) 11%, var(--studio-surface));
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--studio-accent) 31%, transparent);
}
.action-row__dot { width: 7px; height: 7px; }
.action-row__name { font-size: 11px; }
.action-row__count { min-width: 18px; color: var(--studio-muted); font-variant-numeric: tabular-nums; }
.sidebar-add { min-width: 0; gap: 7px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--studio-line); }
.sidebar-add input { min-width: 0; min-height: 34px; }
.sidebar-add .button { width: 100%; min-width: 0; justify-content: center; }
.sidebar-footnote { margin: 11px 3px 0; padding-top: 10px; border-top: 1px solid var(--studio-line); color: var(--studio-muted); font-size: 9px; line-height: 1.55; }

.studio-tabs {
  display: inline-flex;
  align-items: center;
  width: max-content;
  max-width: 100%;
  min-height: 38px;
  gap: 3px;
  padding: 3px;
  border: 1px solid var(--studio-line);
  border-radius: 10px;
  background: color-mix(in srgb, var(--studio-soft) 68%, transparent);
}
.studio-tabs button {
  display: inline-flex;
  min-height: 30px;
  align-items: center;
  justify-content: center;
  margin: 0;
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 0 12px;
  line-height: 1;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0;
}
.studio-tabs button.is-active {
  background: var(--studio-surface);
  box-shadow: 0 2px 7px color-mix(in srgb, #26354a 11%, transparent);
  font-weight: 700;
}
.studio-tabs button.is-active::after { content: none; }

.preview-panel, .timeline-panel, .restyle-panel, .behavior-editor, .inspector-section, .skins-section {
  border-radius: 14px;
  box-shadow: var(--studio-shadow-soft);
}
.preview-panel {
  padding: 18px;
  border-color: color-mix(in srgb, var(--studio-accent) 18%, var(--studio-line));
  background: linear-gradient(145deg, color-mix(in srgb, var(--studio-surface) 94%, transparent), color-mix(in srgb, var(--studio-soft) 50%, transparent));
}
.timeline-panel, .restyle-panel, .behavior-editor, .inspector-section, .skins-section { padding: 15px; }
.timeline-panel, .restyle-panel, .behavior-editor { box-shadow: none; }
.panel-heading { align-items: flex-start; }
.panel-heading--wrap { gap: 14px; }
.panel-heading h3 { font-size: 15px; }
.muted-copy { color: var(--studio-muted); font-size: 10px; font-variant-numeric: tabular-nums; }

.preview-stage {
  min-height: clamp(335px, 34vw, 450px);
  margin-top: 15px;
  border-radius: 11px;
  border-color: color-mix(in srgb, var(--studio-ink) 12%, var(--studio-line));
  box-shadow: inset 0 1px 0 color-mix(in srgb, #fff 74%, transparent);
}
.preview-stage::after {
  position: absolute;
  inset: 12px;
  border: 1px solid color-mix(in srgb, #fff 44%, transparent);
  border-radius: 7px;
  content: '';
  pointer-events: none;
}
.preview-stage img { max-width: 88%; max-height: calc(100% - 34px); filter: drop-shadow(0 16px 10px color-mix(in srgb, #3c2a23 19%, transparent)); }
.preview-stage__loading { right: 14px; bottom: 13px; padding: 6px 9px; box-shadow: 0 4px 12px color-mix(in srgb, #26354a 11%, transparent); }

.timeline-panel { background: color-mix(in srgb, var(--studio-surface) 88%, transparent); }
.timeline-actions { gap: 7px; }
.frame-strip { gap: 10px; margin-top: 14px; padding: 2px 2px 7px; }
.frame-card { width: 86px; height: 104px; flex-basis: 86px; border-radius: 9px; }
.frame-card img { height: 77px; }
.frame-card__duration { padding: 6px; font-size: 10px; }
.frame-card--add { border-radius: 9px; }
.timeline-empty { margin-top: 14px; border-radius: 9px; padding: 26px 20px; color: var(--studio-muted); font-size: 11px; }

.action-detail-grid {
  border-radius: 14px;
  box-shadow: var(--studio-shadow-soft);
}
.detail-section { padding: 16px; }
.detail-section--selected { background: linear-gradient(140deg, var(--studio-surface), color-mix(in srgb, var(--studio-accent) 4%, var(--studio-surface))); }
.section-title-row { align-items: flex-start; }
.section-title-row h3 { font-size: 13px; }
.field { gap: 6px; font-size: 10px; }
.field input, .field select, .field textarea, .sidebar-add input {
  width: 100%;
  border: 1px solid color-mix(in srgb, var(--studio-line) 92%, transparent);
  border-radius: 7px;
  background: color-mix(in srgb, var(--studio-surface) 88%, transparent);
  color: var(--studio-ink);
  font: inherit;
}
.field input, .field select { min-height: 34px; padding: 0 9px; }
.field textarea { min-height: 70px; padding: 8px 9px; line-height: 1.45; resize: vertical; }
.field input:focus, .field select:focus, .field textarea:focus, .sidebar-add input:focus {
  outline: 0;
  border-color: color-mix(in srgb, var(--studio-accent) 58%, var(--studio-line));
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--studio-accent) 12%, transparent);
}
.toggle-line { min-height: 26px; padding: 2px 0; font-size: 11px; }
.check-chip { min-height: 30px; padding: 5px 9px; font-size: 10px; }
.selected-frame-controls { grid-template-columns: 90px minmax(0, 1fr); gap: 12px; }
.selected-frame-preview { min-height: 90px; border-radius: 8px; }
.frame-move-actions { gap: 6px; }
.frame-source { color: var(--studio-muted); font-size: 9px; }

.restyle-panel { gap: 12px; background: color-mix(in srgb, var(--studio-surface) 76%, transparent); }
.behavior-editor { min-height: 470px; background: color-mix(in srgb, var(--studio-surface) 86%, transparent); }
.behavior-layout { grid-template-columns: 170px minmax(0, 1fr); gap: 20px; margin-top: 17px; }
.behavior-list { gap: 4px; padding-right: 12px; border-right: 1px solid var(--studio-line); }
.behavior-list > button { min-height: 36px; border-radius: 7px; padding: 8px 9px; }
.behavior-detail { gap: 16px; }

.studio-inspector { align-self: start; }
.inspector-section { gap: 12px; background: color-mix(in srgb, var(--studio-surface) 91%, transparent); }
.inspector-section:first-child { border-color: color-mix(in srgb, var(--studio-accent) 20%, var(--studio-line)); }
.inspector-heading { gap: 2px; }
.inspector-heading h3 { font-size: 15px; }
.field-grid { gap: 9px; }
.color-row { padding: 8px 0 2px; border-top: 1px solid color-mix(in srgb, var(--studio-line) 72%, transparent); }
.generation-actions { gap: 8px; }
.generation-actions .button--primary { min-height: 38px; }
.step-tracker { flex-wrap: wrap; gap: 5px; }
.step-tracker span { padding: 4px 6px; }
.reference-grid { gap: 8px; }
.reference-thumb, .reference-add { min-height: 66px; border-radius: 8px; }
.reference-thumb img { height: 76px; }
.metadata-list div { padding-top: 8px; }
.save-mode-note { border-left: 2px solid color-mix(in srgb, var(--studio-green) 62%, transparent); padding-left: 8px; color: var(--studio-muted); font-size: 10px; }
.save-actions { gap: 7px; }
.save-actions .button { min-height: 35px; }
.packed-preview { margin-top: 2px; }
.packed-preview img { max-height: 150px; }

.skins-section { padding: 18px; background: color-mix(in srgb, var(--studio-surface) 81%, transparent); }
.skin-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 9px; }
.skin-row {
  min-height: 56px;
  padding: 10px;
  border: 1px solid color-mix(in srgb, var(--studio-line) 88%, transparent);
  border-radius: 9px;
  background: color-mix(in srgb, var(--studio-soft) 52%, transparent);
}
.skin-row > div { flex-direction: column; align-items: flex-start; gap: 2px; }
.skin-row span { max-width: 100%; color: var(--studio-muted); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 9px; }
.skin-row .skin-badge { color: var(--studio-accent); font-family: inherit; }

.button, .icon-button { transition: transform .16s ease, box-shadow .16s ease, border-color .16s ease, background-color .16s ease; }
.button:not(:disabled):hover, .icon-button:not(:disabled):hover { transform: translateY(-1px); box-shadow: 0 5px 12px color-mix(in srgb, #26354a 13%, transparent); }

@media (max-width: 1320px) {
  .studio-layout { grid-template-columns: minmax(165px, 190px) minmax(0, 1fr); }
  .studio-inspector { grid-column: 1 / -1; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; }
  .inspector-section--save { grid-column: 1 / -1; }
}

@media (max-width: 760px) {
  .pet-studio { gap: 13px; padding: 18px 12px 26px; }
  .studio-header { grid-template-columns: 1fr; align-items: start; gap: 14px; padding-bottom: 14px; }
  .studio-title-row h2 { font-size: 26px; }
  .studio-header__source { width: 100%; align-items: flex-end; justify-content: space-between; }
  .field--compact { flex: 1; min-width: 0; }
  .studio-toolbar { align-items: flex-start; flex-direction: column; gap: 9px; }
  .studio-toolbar__actions { width: 100%; justify-content: flex-start; }
  .source-id { max-width: 56vw; }
  .studio-layout { grid-template-columns: 1fr; gap: 14px; }
  .studio-sidebar { position: static; order: 0; }
  .studio-main { order: 1; }
  .studio-inspector { order: 2; display: flex; }
  .action-list { max-height: 245px; }
  .studio-tabs { width: 100%; }
  .studio-tabs button { flex: 1; }
  .preview-panel { padding: 14px; }
  .preview-stage { min-height: 265px; }
  .preview-stage img { max-width: 92%; }
  .panel-heading--wrap { align-items: flex-start; flex-direction: column; }
  .timeline-actions { width: 100%; flex-wrap: wrap; }
  .action-detail-grid, .behavior-layout { grid-template-columns: 1fr; }
  .behavior-list { padding: 0 0 12px; border-right: 0; border-bottom: 1px solid var(--studio-line); }
  .skin-list { grid-template-columns: 1fr; }
  .save-actions { grid-template-columns: 1fr; }
  .save-actions .button--primary { grid-column: auto; }
}
/* 工作台重构：以下规则覆盖旧三栏布局，核心画布与时间线始终优先占用首屏。 */
.pet-studio {
  --studio-workspace-shadow: 0 20px 50px color-mix(in srgb, #152238 13%, transparent);
  --studio-workspace-shadow-soft: 0 10px 28px color-mix(in srgb, #152238 7%, transparent);
  position: relative;
  width: 100%;
  max-width: none;
  min-height: 680px;
  margin: 0;
  padding: clamp(14px, 2vw, 26px);
  gap: 12px;
  overflow: clip;
  background:
    radial-gradient(circle at 72% 14%, color-mix(in srgb, var(--studio-accent) 10%, transparent), transparent 30rem),
    linear-gradient(160deg, color-mix(in srgb, var(--studio-soft) 76%, transparent), transparent 52%);
}

.pet-studio:focus { outline: 0; }
.pet-studio .studio-header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  min-height: 52px;
  gap: 18px;
  padding: 0 0 10px;
  border: 0;
}
.pet-studio .studio-header__identity { display: flex; min-width: 0; align-items: center; gap: 10px; }
.pet-studio .studio-eyebrow { display: none; }
.pet-studio .studio-title-row { min-width: 0; margin: 0; }
.pet-studio .studio-title-row h2 { overflow: hidden; font-size: clamp(20px, 2vw, 26px); line-height: 1.1; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .studio-header__identity > p { display: none; }
.pet-studio .studio-status { flex: 0 0 auto; min-height: 23px; padding: 0 8px; font-size: 10px; }
.pet-studio .studio-header__source {
  min-width: 0;
  gap: 7px;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
}
.pet-studio .field--compact { min-width: 164px; }
.pet-studio .field--compact > span { display: none; }
.pet-studio .field--compact select { min-height: 34px; }

.pet-studio .studio-message {
  position: relative;
  z-index: 5;
  margin: 0;
  border-radius: 9px;
  padding: 8px 11px;
  font-size: 11px;
}
.pet-studio .studio-toolbar {
  display: grid;
  grid-template-columns: minmax(160px, 1fr) auto minmax(0, 1fr);
  align-items: center;
  min-height: 48px;
  gap: 12px;
  padding: 7px 8px 7px 12px;
  border: 1px solid var(--studio-line);
  border-left: 3px solid color-mix(in srgb, var(--studio-accent) 76%, var(--studio-line));
  border-radius: 12px;
  background: color-mix(in srgb, var(--studio-surface) 86%, transparent);
  box-shadow: var(--studio-workspace-shadow-soft);
}
.pet-studio .studio-toolbar__source-copy { display: flex; min-width: 0; align-items: center; gap: 8px; }
.pet-studio .source-kicker { flex: 0 0 auto; font-size: 9px; }
.pet-studio .studio-toolbar__source-copy strong { overflow: hidden; min-width: 0; font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .source-id { display: none; }
.pet-studio .studio-mode-switch,
.pet-studio .studio-inspector__tabs {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 3px;
  padding: 3px;
  border: 1px solid var(--studio-line);
  border-radius: 9px;
  background: color-mix(in srgb, var(--studio-soft) 66%, transparent);
}
.pet-studio .studio-mode-switch button,
.pet-studio .studio-inspector__tabs button {
  display: inline-flex;
  min-height: 28px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin: 0;
  border: 1px solid transparent;
  border-radius: 6px;
  padding: 0 10px;
  background: transparent;
  color: var(--studio-muted);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
}
.pet-studio .studio-mode-switch button.is-active,
.pet-studio .studio-inspector__tabs button.is-active {
  border-color: color-mix(in srgb, var(--studio-accent) 19%, var(--studio-line));
  background: var(--studio-surface);
  box-shadow: 0 2px 7px color-mix(in srgb, #152238 10%, transparent);
  color: var(--studio-ink);
}
.pet-studio .studio-toolbar__actions { display: flex; min-width: 0; justify-content: flex-end; gap: 6px; }
.pet-studio .studio-command,
.pet-studio .studio-compact-button,
.pet-studio .studio-subtle-button,
.pet-studio .studio-icon-button {
  display: inline-flex;
  flex: 0 0 auto;
  min-width: 0;
  min-height: 32px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin: 0;
  border: 1px solid var(--studio-line);
  border-radius: 8px;
  padding: 0 10px;
  background: var(--studio-surface);
  color: var(--studio-ink);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
  transition: border-color .16s ease, background-color .16s ease, box-shadow .16s ease, transform .16s ease;
  white-space: nowrap;
}
.pet-studio .studio-command:hover:not(:disabled),
.pet-studio .studio-compact-button:hover:not(:disabled),
.pet-studio .studio-subtle-button:hover:not(:disabled),
.pet-studio .studio-icon-button:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--studio-accent) 44%, var(--studio-line));
  box-shadow: 0 5px 13px color-mix(in srgb, #152238 12%, transparent);
  transform: translateY(-1px);
}
.pet-studio .studio-command--primary {
  border-color: color-mix(in srgb, var(--studio-green) 52%, var(--studio-line));
  background: var(--studio-green);
  color: #fff;
}
.pet-studio .studio-command--danger,
.pet-studio .studio-icon-button--danger { border-color: color-mix(in srgb, var(--studio-red) 38%, var(--studio-line)); color: var(--studio-red); }
.pet-studio .studio-command--danger { background: color-mix(in srgb, var(--studio-red) 9%, var(--studio-surface)); }
.pet-studio .studio-icon-button { width: 32px; padding: 0; }
.pet-studio .operation-label { flex: 0 0 auto; font-size: 10px; white-space: nowrap; }

.pet-studio .studio-layout {
  display: grid;
  grid-template-columns: 180px minmax(0, 1fr);
  align-items: stretch;
  gap: 12px;
}
.pet-studio .studio-sidebar {
  position: sticky;
  top: 12px;
  display: flex;
  min-height: 0;
  max-height: calc(100vh - 118px);
  flex-direction: column;
  gap: 10px;
  padding: 11px 8px 9px;
  border: 1px solid var(--studio-line);
  border-radius: 12px;
  background: color-mix(in srgb, var(--studio-surface) 84%, transparent);
  box-shadow: var(--studio-workspace-shadow-soft);
}
.pet-studio .sidebar-heading { min-height: 33px; align-items: center; padding: 0 4px; }
.pet-studio .sidebar-heading h3 { margin: 1px 0 0; font-size: 13px; }
.pet-studio .count-badge { width: 25px; height: 25px; padding: 0; font-size: 9px; }
.pet-studio .action-list { max-height: none; flex: 1; margin: 0; overflow: auto; padding: 1px; }
.pet-studio .action-row { min-height: 34px; margin: 0 0 2px; border: 0; border-radius: 7px; }
.pet-studio .action-row__select { min-height: 34px; border-radius: 7px; padding: 7px; }
.pet-studio .action-row.is-selected .action-row__select { background: color-mix(in srgb, var(--studio-accent) 11%, var(--studio-surface)); box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--studio-accent) 28%, transparent); }
.pet-studio .action-row__dot { width: 7px; height: 7px; flex-basis: 7px; }
.pet-studio .action-row__name { font-size: 11px; }
.pet-studio .behavior-rail-list { display: flex; flex-direction: column; gap: 2px; }
.pet-studio .behavior-rail-row {
  display: flex;
  min-height: 34px;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 7px;
  background: transparent;
  color: var(--studio-ink);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  text-align: left;
}
.pet-studio .behavior-rail-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 28%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 9%, var(--studio-surface)); }
.pet-studio .behavior-rail-row small { color: var(--studio-muted); font-variant-numeric: tabular-nums; }
.pet-studio .sidebar-add { margin: 0; padding: 9px 3px 0; border-top: 1px solid var(--studio-line); }
.pet-studio .sidebar-add input { min-height: 32px; padding: 0 8px; }
.pet-studio .sidebar-add .button { width: 100%; min-height: 31px; margin: 0; white-space: nowrap; }
.pet-studio .sidebar-footnote { display: none; }

.pet-studio .studio-main { display: flex; min-width: 0; flex-direction: column; gap: 12px; }
.pet-studio .studio-tabs { display: none; }
.pet-studio .preview-panel,
.pet-studio .timeline-panel,
.pet-studio .behavior-editor {
  margin: 0;
  border: 1px solid var(--studio-line);
  border-radius: 12px;
  padding: 14px;
  background: color-mix(in srgb, var(--studio-surface) 89%, transparent);
  box-shadow: var(--studio-workspace-shadow-soft);
}
.pet-studio .preview-panel { display: flex; min-height: min(62vh, 610px); flex: 1; flex-direction: column; }
.pet-studio .panel-heading { min-height: 30px; align-items: center; }
.pet-studio .panel-heading h3 { font-size: 14px; }
.pet-studio .preview-stage { min-height: 0; flex: 1; margin-top: 10px; border-radius: 9px; }
.pet-studio .preview-stage img { max-width: 88%; max-height: 88%; filter: drop-shadow(0 18px 14px color-mix(in srgb, #1d2733 22%, transparent)); }
.pet-studio .preview-stage__empty { max-width: 260px; gap: 7px; }
.pet-studio .preview-stage__empty-mark { width: 42px; height: 42px; }
.pet-studio .preview-stage__loading { right: 12px; bottom: 12px; }
.pet-studio .timeline-panel { padding: 12px 14px 10px; }
.pet-studio .timeline-actions { gap: 6px; }
.pet-studio .timeline-frame-tools {
  display: flex;
  min-height: 37px;
  align-items: center;
  gap: 9px;
  margin-top: 10px;
  padding: 5px 7px 5px 9px;
  border: 1px solid color-mix(in srgb, var(--studio-line) 82%, transparent);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-soft) 52%, transparent);
}
.pet-studio .timeline-frame-tools > span { color: var(--studio-muted); font-size: 10px; font-weight: 700; white-space: nowrap; }
.pet-studio .timeline-frame-tools label { display: inline-flex; align-items: center; gap: 6px; color: var(--studio-muted); font-size: 10px; font-weight: 700; }
.pet-studio .timeline-frame-tools input { width: 84px; min-height: 27px; border: 1px solid var(--studio-line); border-radius: 6px; padding: 0 7px; background: var(--studio-surface); color: var(--studio-ink); font: inherit; font-size: 10px; }
.pet-studio .timeline-frame-tools__actions { display: flex; gap: 5px; margin-left: auto; }
.pet-studio .timeline-frame-tools__actions .studio-icon-button { width: 28px; min-height: 28px; }
.pet-studio .frame-strip { gap: 8px; margin-top: 10px; padding: 1px 1px 5px; }
.pet-studio .frame-card { width: 78px; height: 92px; flex-basis: 78px; border-radius: 7px; }
.pet-studio .frame-card img { height: 64px; }
.pet-studio .frame-card__duration { padding: 5px; font-size: 9px; }
.pet-studio .frame-card--add { border-style: dashed; }
.pet-studio .timeline-empty { margin-top: 9px; padding: 20px; }

.pet-studio .behavior-editor { min-height: 540px; }
.pet-studio .behavior-layout { grid-template-columns: minmax(0, 1fr); gap: 18px; margin-top: 12px; }
.pet-studio .studio-main .behavior-list { display: none; }
.pet-studio .behavior-list { padding-right: 10px; border-right: 1px solid var(--studio-line); }
.pet-studio .behavior-list > button { min-height: 35px; border-radius: 7px; }
.pet-studio .behavior-detail { gap: 14px; }
.pet-studio .behavior-order-row { min-height: 38px; }

.pet-studio .studio-scrim {
  position: fixed;
  z-index: 90;
  inset: 0;
  width: 100%;
  height: 100%;
  margin: 0;
  border: 0;
  background: color-mix(in srgb, #102034 20%, transparent);
  cursor: default;
}
.pet-studio .studio-inspector,
.pet-studio .studio-library,
.pet-studio .studio-save-menu {
  position: fixed;
  z-index: 100;
  border: 1px solid color-mix(in srgb, var(--studio-line) 88%, #fff);
  border-radius: 14px;
  background: var(--studio-surface);
  box-shadow: var(--studio-workspace-shadow);
  animation: studio-panel-in .18s ease-out both;
}
.pet-studio .studio-inspector {
  top: 74px;
  right: 22px;
  bottom: 22px;
  display: flex;
  width: min(372px, calc(100vw - 44px));
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
}
.pet-studio .studio-inspector__header,
.pet-studio .studio-save-menu__heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 13px 14px 10px;
  border-bottom: 1px solid var(--studio-line);
}
.pet-studio .studio-inspector__header strong { display: block; margin-top: 3px; font-size: 14px; }
.pet-studio .studio-inspector__tabs { flex: 0 0 auto; margin: 10px 12px 0; }
.pet-studio .studio-inspector__tabs button { flex: 1; padding: 0 8px; }
.pet-studio .studio-inspector__body { min-height: 0; flex: 1; overflow: auto; padding: 12px; }
.pet-studio .inspector-section { gap: 10px; padding: 0; border: 0; border-radius: 0; box-shadow: none; }
.pet-studio .inspector-section + .inspector-section { margin-top: 16px; padding-top: 15px; border-top: 1px solid var(--studio-line); }
.pet-studio .inspector-heading { min-height: 0; }
.pet-studio .inspector-heading h3 { margin-top: 2px; font-size: 13px; }
.pet-studio .field input,
.pet-studio .field textarea,
.pet-studio .field select,
.pet-studio .sidebar-add input {
  border-radius: 7px;
  background: color-mix(in srgb, var(--studio-surface) 92%, transparent);
}
.pet-studio .field input,
.pet-studio .field select { min-height: 33px; }
.pet-studio .field textarea { min-height: 68px; }
.pet-studio .inspector-divider { height: 1px; margin: 3px 0; background: var(--studio-line); }
.pet-studio .inspector-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 7px; }
.pet-studio .inspector-actions .studio-subtle-button { min-height: 34px; padding: 0 8px; font-size: 10px; }
.pet-studio .restyle-options { gap: 5px; }
.pet-studio .check-chip { min-height: 28px; padding: 4px 7px; }
.pet-studio .reference-grid { gap: 7px; }
.pet-studio .reference-thumb,
.pet-studio .reference-add { min-height: 66px; border-radius: 8px; }
.pet-studio .reference-thumb img { height: 74px; }
.pet-studio .generation-actions .button--primary { min-height: 38px; }
.pet-studio .save-actions .button { min-height: 34px; }
.pet-studio .packed-preview img { max-height: 140px; }

.pet-studio .studio-library {
  top: 74px;
  right: 22px;
  width: min(520px, calc(100vw - 44px));
  max-height: calc(100vh - 96px);
  overflow: auto;
  padding: 14px;
}
.pet-studio .studio-library .panel-heading { align-items: center; }
.pet-studio .studio-library__heading-actions { display: flex; gap: 6px; }
.pet-studio .studio-library .skin-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
.pet-studio .studio-library .skin-row {
  display: grid;
  min-height: 70px;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 7px;
  margin: 0;
  padding: 8px;
  border: 1px solid var(--studio-line);
  border-radius: 9px;
  background: color-mix(in srgb, var(--studio-soft) 46%, transparent);
}
.pet-studio .studio-library .skin-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 52%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 8%, var(--studio-surface)); }
.pet-studio .skin-row__select {
  display: flex;
  min-width: 0;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  border: 0;
  padding: 2px;
  background: transparent;
  color: var(--studio-ink);
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.pet-studio .skin-row__select strong,
.pet-studio .skin-row__select span { overflow: hidden; max-width: 100%; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .skin-row__select strong { font-size: 11px; }
.pet-studio .skin-row__select span { color: var(--studio-muted); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 9px; }

.pet-studio .studio-save-menu {
  top: 74px;
  right: 22px;
  width: min(330px, calc(100vw - 44px));
  padding: 0 14px 14px;
}
.pet-studio .studio-save-menu__heading { margin: 0 -14px 12px; }
.pet-studio .studio-save-menu__heading h3 { margin: 2px 0 0; font-size: 14px; }
.pet-studio .studio-save-menu > p { margin: 8px 0 12px; color: var(--studio-muted); font-size: 11px; line-height: 1.5; }
.pet-studio .studio-save-menu__actions { display: grid; grid-template-columns: 1fr 1fr; gap: 7px; }
.pet-studio .studio-save-menu__actions .studio-command { grid-column: 1 / -1; }

.pet-studio .studio-compact-button:disabled,
.pet-studio .studio-command:disabled,
.pet-studio .studio-subtle-button:disabled,
.pet-studio .studio-icon-button:disabled { cursor: not-allowed; opacity: .45; transform: none; box-shadow: none; }

@keyframes studio-panel-in {
  from { opacity: 0; transform: translateX(14px) scale(.985); }
  to { opacity: 1; transform: translateX(0) scale(1); }
}

@media (max-width: 1100px) {
  .pet-studio .studio-toolbar { grid-template-columns: minmax(0, 1fr) auto; }
  .pet-studio .studio-mode-switch { order: 3; grid-column: 1 / -1; justify-self: start; }
  .pet-studio .studio-layout { grid-template-columns: 160px minmax(0, 1fr); }
  .pet-studio .studio-sidebar { max-height: calc(100vh - 162px); }
}

@media (max-width: 760px) {
  .pet-studio { min-height: 0; padding: 12px; overflow: visible; }
  .pet-studio .studio-header { grid-template-columns: 1fr; gap: 10px; }
  .pet-studio .studio-header__source { width: 100%; justify-content: flex-start; flex-wrap: wrap; }
  .pet-studio .field--compact { flex: 1; min-width: 140px; }
  .pet-studio .studio-toolbar { display: flex; align-items: flex-start; flex-direction: column; gap: 9px; }
  .pet-studio .studio-toolbar__source-copy { width: 100%; }
  .pet-studio .studio-toolbar__actions { width: 100%; justify-content: flex-start; overflow-x: auto; padding-bottom: 2px; }
  .pet-studio .studio-mode-switch { width: 100%; }
  .pet-studio .studio-mode-switch button { flex: 1; }
  .pet-studio .studio-layout { grid-template-columns: 1fr; gap: 10px; }
  .pet-studio .studio-sidebar { position: static; max-height: none; padding: 8px; }
  .pet-studio .sidebar-heading { display: none; }
  .pet-studio .action-list { display: flex; min-height: 36px; flex: 0 0 auto; overflow-x: auto; padding: 0; }
  .pet-studio .action-row { flex: 0 0 auto; margin: 0 4px 0 0; }
  .pet-studio .action-row__select { padding: 7px 9px; }
  .pet-studio .action-row__count { display: none; }
  .pet-studio .behavior-rail-list { flex-direction: row; }
  .pet-studio .behavior-rail-row { flex: 0 0 auto; }
  .pet-studio .sidebar-add { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 6px; padding: 0; border: 0; }
  .pet-studio .sidebar-add .button { width: auto; }
  .pet-studio .preview-panel { min-height: 355px; padding: 11px; }
  .pet-studio .timeline-panel { padding: 11px; }
  .pet-studio .panel-heading--wrap { align-items: flex-start; flex-direction: column; }
  .pet-studio .timeline-actions { width: 100%; justify-content: flex-start; }
  .pet-studio .timeline-frame-tools { flex-wrap: wrap; }
  .pet-studio .timeline-frame-tools__actions { margin-left: 0; }
  .pet-studio .behavior-editor { min-height: 480px; }
  .pet-studio .behavior-layout { grid-template-columns: 1fr; }
  .pet-studio .studio-main .behavior-list { display: none; }
  .pet-studio .studio-inspector,
  .pet-studio .studio-library,
  .pet-studio .studio-save-menu {
    top: auto;
    right: 0;
    bottom: 0;
    left: 0;
    width: 100%;
    max-height: min(82vh, 760px);
    border-right: 0;
    border-bottom: 0;
    border-left: 0;
    border-radius: 16px 16px 0 0;
  }
  .pet-studio .studio-inspector { min-width: 0; }
  .pet-studio .studio-library .skin-list { grid-template-columns: 1fr; }
  .pet-studio .studio-save-menu__actions { grid-template-columns: 1fr; }
  .pet-studio .studio-save-menu__actions .studio-command { grid-column: auto; }
}

@media (prefers-reduced-motion: reduce) {
  .pet-studio .studio-inspector,
  .pet-studio .studio-library,
  .pet-studio .studio-save-menu { animation: none; }
}

/* OpenCowork 形象工坊基线：项目设置 -> 动作列表 -> 时间线，保存条固定在编辑区底部。 */
.pet-studio {
  max-width: 1480px;
  min-height: 0;
  margin: 0 auto;
  padding: 18px;
  background: transparent;
}
.pet-studio .studio-header {
  display: flex;
  min-height: 42px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding: 0;
}
.pet-studio .studio-header__identity { display: flex; min-width: 0; align-items: center; gap: 10px; }
.pet-studio .studio-header__identity > p,
.pet-studio .studio-eyebrow,
.pet-studio .studio-header__source,
.pet-studio .studio-toolbar { display: none !important; }
.pet-studio .studio-title-row { min-width: 0; margin: 0; }
.pet-studio .studio-title-row h2 { font-size: 18px; letter-spacing: -.02em; }
.pet-studio .studio-status { min-height: 22px; padding: 0 8px; font-size: 10px; }
.pet-studio .studio-mode-switch--header { display: inline-flex; flex: 0 0 auto; }
.pet-studio .studio-mode-switch--header button { min-height: 29px; }

.pet-studio .studio-layout {
  display: grid;
  grid-template-columns: 300px 220px minmax(0, 1fr);
  align-items: start;
  gap: 14px;
}
.pet-studio .studio-inspector {
  position: static;
  z-index: auto;
  grid-column: 1;
  grid-row: 1;
  display: block;
  width: auto;
  min-width: 0;
  max-height: none;
  overflow: visible;
  border: 1px solid var(--studio-line);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-soft) 24%, var(--studio-surface));
  box-shadow: none;
  animation: none;
}
.pet-studio .studio-inspector__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 42px;
  padding: 10px 12px;
  border-bottom: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent);
}
.pet-studio .studio-inspector__header strong { display: block; margin-top: 2px; font-size: 12px; }
.pet-studio .studio-inspector__tabs { display: none; }
.pet-studio .studio-settings-toggle {
  display: inline-grid;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
  place-items: center;
  border: 1px solid var(--studio-line);
  border-radius: 5px;
  padding: 0;
  background: var(--studio-surface);
  color: var(--studio-muted);
  cursor: pointer;
  font: inherit;
  font-size: 16px;
  line-height: 1;
}
.pet-studio .studio-settings-toggle:hover:not(:disabled) { border-color: color-mix(in srgb, var(--studio-accent) 52%, var(--studio-line)); color: var(--studio-ink); }
.pet-studio .studio-inspector__body { display: block; overflow: visible; padding: 10px; }
.pet-studio .inspector-section { display: flex; gap: 8px; padding: 10px; border: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent); border-radius: 7px; background: color-mix(in srgb, var(--studio-surface) 76%, transparent); }
.pet-studio .inspector-section + .inspector-section { margin-top: 10px; padding-top: 10px; border-top: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent); }
.pet-studio .inspector-section--save { display: none; }
.pet-studio .inspector-heading { min-height: 0; }
.pet-studio .inspector-heading h3 { font-size: 12px; }
.pet-studio .inspector-heading .section-kicker { font-size: 9px; }
.pet-studio .studio-settings-hint { margin: 0; color: var(--studio-muted); font-size: 10px; line-height: 1.45; }
.pet-studio .field { gap: 4px; font-size: 10px; }
.pet-studio .field input,
.pet-studio .field select,
.pet-studio .field textarea { border-radius: 6px; font-size: 11px; }
.pet-studio .field input,
.pet-studio .field select { min-height: 32px; }
.pet-studio .field textarea { min-height: 62px; }
.pet-studio .reference-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
.pet-studio .reference-thumb,
.pet-studio .reference-add { min-height: 60px; }
.pet-studio .reference-thumb img { height: 60px; }
.pet-studio .generation-actions .button--primary { min-height: 34px; }
.pet-studio .inspector-actions { grid-template-columns: 1fr; }
.pet-studio .inspector-actions .studio-subtle-button { justify-content: flex-start; padding: 0 9px; }
.pet-studio .check-chip { min-height: 25px; padding: 3px 6px; font-size: 9px; }
.pet-studio .studio-skin-library { border-top: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent); padding-top: 8px; }
.pet-studio .studio-skin-library summary { color: var(--studio-muted); cursor: pointer; font-size: 10px; font-weight: 700; }
.pet-studio .studio-skin-library__actions { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 8px; color: var(--studio-muted); font-size: 10px; }
.pet-studio .studio-skin-library__actions .studio-icon-button { width: 26px; min-height: 26px; }
.pet-studio .studio-skin-list { display: flex; flex-direction: column; gap: 5px; max-height: 210px; margin-top: 7px; overflow: auto; }
.pet-studio .studio-skin-row { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; align-items: center; gap: 5px; border: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent); border-radius: 5px; padding: 5px; background: color-mix(in srgb, var(--studio-soft) 38%, transparent); }
.pet-studio .studio-skin-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 48%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 7%, var(--studio-surface)); }
.pet-studio .studio-skin-row > button:first-child { display: flex; min-width: 0; flex-direction: column; align-items: flex-start; gap: 1px; border: 0; padding: 1px; background: transparent; color: var(--studio-ink); cursor: pointer; font: inherit; text-align: left; }
.pet-studio .studio-skin-row strong,
.pet-studio .studio-skin-row small { overflow: hidden; max-width: 100%; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .studio-skin-row strong { font-size: 10px; }
.pet-studio .studio-skin-row small { color: var(--studio-muted); font-size: 9px; }
.pet-studio .studio-skin-row .studio-icon-button { width: 26px; min-height: 26px; }

.pet-studio .studio-sidebar {
  position: static;
  grid-column: 2;
  grid-row: 1;
  min-height: 610px;
  max-height: none;
  padding: 10px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-soft) 22%, var(--studio-surface));
  box-shadow: none;
}
.pet-studio .sidebar-heading { min-height: 29px; padding: 0 2px 7px; }
.pet-studio .sidebar-heading h3 { font-size: 12px; }
.pet-studio .count-badge { width: 23px; height: 23px; }
.pet-studio .action-list { flex: 0 1 auto; overflow: auto; }
.pet-studio .action-row,
.pet-studio .behavior-rail-row { min-height: 37px; border: 1px solid transparent; border-radius: 6px; }
.pet-studio .action-row.is-selected,
.pet-studio .behavior-rail-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 52%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 9%, var(--studio-surface)); }
.pet-studio .action-row.is-selected .action-row__select { box-shadow: none; background: transparent; }
.pet-studio .action-row__select { min-height: 35px; padding: 7px 8px; }
.pet-studio .behavior-rail-row { padding: 7px 8px; }
.pet-studio .sidebar-add { margin-top: auto; padding: 10px 0 0; }
.pet-studio .sidebar-add .button { min-height: 32px; border-radius: 6px; }

.pet-studio .studio-main {
  grid-column: 3;
  grid-row: 1;
  min-width: 0;
  gap: 12px;
}
.pet-studio .studio-tabs { display: none; }
.pet-studio .preview-panel,
.pet-studio .timeline-panel,
.pet-studio .behavior-editor { border-radius: 8px; background: color-mix(in srgb, var(--studio-surface) 90%, transparent); box-shadow: none; }
.pet-studio .preview-panel { min-height: 438px; padding: 12px; }
.pet-studio .preview-stage { min-height: 380px; border-radius: 7px; }
.pet-studio .preview-stage img { max-width: 84%; max-height: 84%; filter: none; }
.pet-studio .panel-heading h3 { font-size: 13px; }
.pet-studio .timeline-panel { padding: 12px; }
.pet-studio .timeline-frame-tools { min-height: 34px; margin-top: 8px; padding: 4px 6px 4px 8px; border-radius: 6px; }
.pet-studio .timeline-frame-tools input { min-height: 26px; }
.pet-studio .frame-strip { gap: 7px; margin-top: 8px; }
.pet-studio .frame-card { width: 72px; height: 86px; flex-basis: 72px; border-radius: 6px; }
.pet-studio .frame-card img { height: 58px; }
.pet-studio .behavior-editor { min-height: 610px; padding: 14px; }
.pet-studio .studio-layout.is-behavior .studio-sidebar { grid-column: 2; }
.pet-studio .studio-layout.is-behavior .studio-main { grid-column: 3; }

.pet-studio .studio-savebar {
  position: sticky;
  z-index: 8;
  bottom: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid color-mix(in srgb, var(--studio-line) 86%, transparent);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-surface) 94%, transparent);
  box-shadow: 0 10px 24px color-mix(in srgb, #152238 12%, transparent);
  backdrop-filter: blur(10px);
}
.pet-studio .studio-savebar__status,
.pet-studio .studio-savebar__actions { display: flex; align-items: center; gap: 9px; }
.pet-studio .studio-savebar__status { color: var(--studio-muted); font-size: 10px; }
.pet-studio .studio-savebar__status .is-dirty { color: #ae6d20; }
.pet-studio .studio-savebar__actions { flex-wrap: wrap; justify-content: flex-end; }
.pet-studio .studio-savebar .studio-command,
.pet-studio .studio-savebar .studio-subtle-button { min-height: 31px; }

@media (max-width: 1180px) {
  .pet-studio .studio-layout { grid-template-columns: 272px 184px minmax(0, 1fr); gap: 10px; }
  .pet-studio .preview-panel { min-height: 380px; }
  .pet-studio .preview-stage { min-height: 330px; }
}

@media (max-width: 900px) {
  .pet-studio .studio-layout { grid-template-columns: 250px minmax(0, 1fr); }
  .pet-studio .studio-inspector { grid-column: 1; }
  .pet-studio .studio-sidebar { grid-column: 1; grid-row: 2; min-height: 0; }
  .pet-studio .studio-main { grid-column: 2; grid-row: 1 / span 2; }
  .pet-studio .studio-layout.is-behavior .studio-sidebar { grid-column: 1; grid-row: 2; }
  .pet-studio .studio-layout.is-behavior .studio-main { grid-column: 2; grid-row: 1 / span 2; }
}

@media (max-width: 700px) {
  .pet-studio { padding: 12px; }
  .pet-studio .studio-header { align-items: flex-start; flex-direction: column; }
  .pet-studio .studio-mode-switch--header { width: 100%; }
  .pet-studio .studio-mode-switch--header button { flex: 1; }
  .pet-studio .studio-layout { grid-template-columns: 1fr; }
  .pet-studio .studio-inspector,
  .pet-studio .studio-sidebar,
  .pet-studio .studio-main,
  .pet-studio .studio-layout.is-behavior .studio-sidebar,
  .pet-studio .studio-layout.is-behavior .studio-main { grid-column: 1; grid-row: auto; }
  .pet-studio .studio-sidebar { min-height: 0; }
  .pet-studio .action-list { display: flex; flex-direction: row; overflow-x: auto; }
  .pet-studio .action-row,
  .pet-studio .behavior-rail-row { flex: 0 0 auto; min-width: 112px; }
  .pet-studio .sidebar-add { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 6px; }
  .pet-studio .sidebar-add .button { width: auto; }
  .pet-studio .preview-panel { min-height: 320px; }
  .pet-studio .preview-stage { min-height: 270px; }
  .pet-studio .studio-savebar { align-items: flex-start; flex-direction: column; }
  .pet-studio .studio-savebar__actions { width: 100%; justify-content: flex-start; }
}
/* 最终工作台覆盖层：把项目设置、动作列表、预览和当前动作操作固定为同一工作区。 */
.pet-studio .studio-layout {
  display: grid;
  grid-template-columns: 300px 220px minmax(0, 1fr) 198px;
  align-items: start;
  gap: 12px;
}

.pet-studio .studio-inspector {
  position: static;
  z-index: auto;
  grid-column: 1;
  grid-row: 1;
  display: block;
  width: auto;
  max-height: none;
  overflow: visible;
  border: 1px solid var(--studio-line);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-soft) 24%, var(--studio-surface));
  box-shadow: none;
  animation: none;
}

.pet-studio .studio-inspector__body {
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: visible;
  padding: 8px;
}

.pet-studio .inspector-section {
  display: flex;
  gap: 7px;
  margin: 0;
  padding: 10px;
  border: 1px solid color-mix(in srgb, var(--studio-line) 78%, transparent);
  border-radius: 7px;
  background: color-mix(in srgb, var(--studio-surface) 78%, transparent);
  box-shadow: none;
}

.pet-studio .inspector-section + .inspector-section {
  margin-top: 0;
  padding-top: 10px;
  border-top: 1px solid color-mix(in srgb, var(--studio-line) 78%, transparent);
}

.pet-studio .inspector-heading h3 { font-size: 12px; }
.pet-studio .inspector-heading .section-kicker { font-size: 9px; }
.pet-studio .field { gap: 4px; font-size: 10px; }
.pet-studio .field input,
.pet-studio .field select,
.pet-studio .field textarea { border-radius: 6px; font-size: 11px; }
.pet-studio .field input,
.pet-studio .field select { min-height: 31px; }
.pet-studio .field textarea { min-height: 58px; }
.pet-studio .inspector-section--metadata .field textarea { min-height: 54px; }
.pet-studio .studio-settings-hint { margin: 0; color: var(--studio-muted); font-size: 10px; line-height: 1.4; }
.pet-studio .reference-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 6px; }
.pet-studio .reference-thumb,
.pet-studio .reference-add { min-height: 58px; border-radius: 6px; }
.pet-studio .reference-thumb img { height: 58px; }
.pet-studio .reference-add { gap: 4px; padding: 5px; }
.pet-studio .reference-add span { font-size: 9px; text-align: center; }
.pet-studio .generation-actions { gap: 6px; }
.pet-studio .generation-actions .button--primary { min-height: 34px; }
.pet-studio .generation-actions .button { white-space: nowrap; }
.pet-studio .studio-advanced-settings {
  border-top: 1px solid color-mix(in srgb, var(--studio-line) 76%, transparent);
  padding-top: 7px;
}
.pet-studio .studio-advanced-settings summary {
  color: var(--studio-muted);
  cursor: pointer;
  font-size: 10px;
  font-weight: 700;
}
.pet-studio .studio-advanced-settings__body { display: flex; flex-direction: column; gap: 7px; margin-top: 8px; }
.pet-studio .studio-advanced-settings__body .field textarea { min-height: 54px; }
.pet-studio .step-tracker { flex-wrap: wrap; }

.pet-studio .studio-sidebar {
  position: static;
  grid-column: 2;
  grid-row: 1;
  min-height: 438px;
  max-height: none;
  padding: 10px 8px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-soft) 22%, var(--studio-surface));
  box-shadow: none;
}

.pet-studio .sidebar-heading { min-height: 30px; padding: 0 3px 7px; }
.pet-studio .sidebar-heading h3 { font-size: 12px; }
.pet-studio .sidebar-add-trigger { min-height: 28px; padding: 0 7px; font-size: 10px; }
.pet-studio .action-list { flex: 0 1 auto; margin: 0; overflow: auto; }
.pet-studio .action-row { min-height: 58px; margin: 0 0 4px; border: 1px solid transparent; border-radius: 7px; }
.pet-studio .action-row__select { min-height: 56px; align-items: center; gap: 7px; padding: 6px; }
.pet-studio .action-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 52%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 8%, var(--studio-surface)); }
.pet-studio .action-row.is-selected .action-row__select { background: transparent; box-shadow: none; }
.pet-studio .action-row__art {
  display: grid;
  width: 42px;
  height: 42px;
  flex: 0 0 42px;
  place-items: center;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--studio-line) 86%, transparent);
  border-radius: 5px;
  background: #eef1ef;
  background-image: linear-gradient(45deg, #dfe5e1 25%, transparent 25%), linear-gradient(-45deg, #dfe5e1 25%, transparent 25%), linear-gradient(45deg, transparent 75%, #dfe5e1 75%), linear-gradient(-45deg, transparent 75%, #dfe5e1 75%);
  background-position: 0 0, 0 6px, 6px -6px, -6px 0;
  background-size: 12px 12px;
  color: var(--studio-muted);
}
.pet-studio .action-row__art img { width: 100%; height: 100%; object-fit: contain; }
.pet-studio .action-row__copy { display: flex; min-width: 0; flex: 1; flex-direction: column; align-items: flex-start; gap: 2px; }
.pet-studio .action-row__copy strong,
.pet-studio .action-row__copy small,
.pet-studio .action-row__copy em { max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .action-row__copy strong { font-size: 11px; font-weight: 700; }
.pet-studio .action-row__copy small { color: var(--studio-muted); font-size: 9px; }
.pet-studio .action-row__copy em { color: var(--studio-muted); font-size: 9px; font-style: normal; }
.pet-studio .action-row__delete { width: 22px; flex: 0 0 22px; font-size: 15px; }
.pet-studio .sidebar-add { margin-top: auto; padding: 9px 2px 0; border-top: 1px solid var(--studio-line); }
.pet-studio .sidebar-add .button { width: 100%; min-height: 31px; white-space: nowrap; }
.pet-studio .sidebar-footnote { display: none; }

.pet-studio .studio-main {
  grid-column: 3;
  grid-row: 1;
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 10px;
}
.pet-studio .studio-tabs { display: none; }
.pet-studio .studio-action-workspace { display: flex; min-width: 0; flex-direction: column; gap: 10px; }
.pet-studio .preview-panel { min-height: 0; padding: 0; border: 0; background: transparent; box-shadow: none; }
.pet-studio .preview-panel > .panel-heading { display: none; }
.pet-studio .preview-stage { width: 100%; min-height: 0; aspect-ratio: 1; margin: 0; border-radius: 8px; }
.pet-studio .preview-stage img { max-width: 86%; max-height: 86%; filter: none; }
.pet-studio .preview-stage__empty { max-width: 180px; }
.pet-studio .preview-stage__empty strong { font-size: 11px; }
.pet-studio .preview-stage__empty small { font-size: 9px; }
.pet-studio .timeline-panel { padding: 10px; border-radius: 8px; box-shadow: none; }
.pet-studio .timeline-panel .panel-heading h3 { font-size: 12px; }
.pet-studio .timeline-actions { gap: 5px; }
.pet-studio .timeline-actions .studio-subtle-button { min-height: 28px; padding: 0 7px; font-size: 10px; }
.pet-studio .timeline-frame-tools { min-height: 32px; margin-top: 7px; padding: 4px 6px; border-radius: 6px; }
.pet-studio .frame-strip { gap: 6px; margin-top: 7px; }
.pet-studio .frame-card { width: 68px; height: 82px; flex-basis: 68px; border-radius: 6px; }
.pet-studio .frame-card img { height: 56px; }

.pet-studio .studio-action-inspector {
  grid-column: 4;
  grid-row: 1;
  display: flex;
  min-height: 438px;
  min-width: 0;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--studio-line);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-surface) 90%, transparent);
}
.pet-studio .studio-action-inspector__header { position: relative; display: flex; min-width: 0; flex-direction: column; align-items: flex-start; gap: 3px; padding: 11px 36px 9px 10px; border-bottom: 1px solid var(--studio-line); }
.pet-studio .studio-action-inspector__title { display: flex; width: 100%; min-width: 0; align-items: baseline; justify-content: space-between; gap: 7px; }
.pet-studio .studio-action-inspector__title strong { overflow: hidden; min-width: 0; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
.pet-studio .studio-action-inspector__title span { flex: 0 0 auto; color: var(--studio-muted); font-size: 9px; white-space: nowrap; }
.pet-studio .studio-action-inspector__header > .studio-icon-button { position: absolute; top: 8px; right: 7px; width: 26px; min-height: 26px; }
.pet-studio .studio-action-inspector__body { display: flex; flex-direction: column; gap: 10px; padding: 10px; }
.pet-studio .studio-action-commands { display: flex; flex-direction: column; gap: 6px; }
.pet-studio .studio-action-command {
  display: inline-flex;
  width: 100%;
  min-height: 34px;
  align-items: center;
  justify-content: flex-start;
  gap: 6px;
  overflow: hidden;
  border: 1px solid var(--studio-line);
  border-radius: 6px;
  padding: 0 8px;
  background: var(--studio-surface);
  color: var(--studio-ink);
  cursor: pointer;
  font: inherit;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pet-studio .studio-action-command:hover:not(:disabled) { border-color: color-mix(in srgb, var(--studio-accent) 45%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 6%, var(--studio-surface)); }
.pet-studio .studio-action-command--primary { border-color: color-mix(in srgb, var(--studio-accent) 38%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 8%, var(--studio-surface)); }
.pet-studio .studio-action-command:disabled { cursor: not-allowed; opacity: .45; }
.pet-studio .studio-action-inspector__hint { margin: 0; color: var(--studio-muted); font-size: 10px; line-height: 1.5; }
.pet-studio .studio-action-details { border-top: 1px solid var(--studio-line); padding-top: 8px; }
.pet-studio .studio-action-details summary { color: var(--studio-ink); cursor: pointer; font-size: 10px; font-weight: 700; }
.pet-studio .studio-action-details__body { display: flex; flex-direction: column; gap: 8px; margin-top: 8px; }
.pet-studio .studio-action-details .field textarea { min-height: 54px; }
.pet-studio .studio-action-details .check-chip { max-width: 100%; min-height: 25px; padding: 3px 6px; font-size: 9px; }
.pet-studio .studio-action-restyle-button { width: 100%; min-height: 30px; font-size: 10px; }

.pet-studio .studio-layout.is-behavior {
  grid-template-columns: 300px 220px minmax(0, 1fr);
}
.pet-studio .studio-layout.is-behavior .studio-inspector { grid-column: 1; grid-row: 1; }
.pet-studio .studio-layout.is-behavior .studio-sidebar { grid-column: 2; grid-row: 1; }
.pet-studio .studio-layout.is-behavior .studio-main { grid-column: 3; grid-row: 1; }

@media (max-width: 1180px) {
  .pet-studio .studio-layout { grid-template-columns: 260px 200px minmax(0, 1fr) 190px; gap: 10px; }
  .pet-studio .studio-layout.is-behavior { grid-template-columns: 260px 200px minmax(0, 1fr); }
}

@media (max-width: 900px) {
  .pet-studio .studio-layout,
  .pet-studio .studio-layout.is-behavior { grid-template-columns: 250px minmax(0, 1fr); gap: 10px; }
  .pet-studio .studio-inspector { grid-column: 1; grid-row: 1; }
  .pet-studio .studio-sidebar { grid-column: 1; grid-row: 2; min-height: 0; }
  .pet-studio .studio-main { grid-column: 2; grid-row: 1 / span 2; }
  .pet-studio .studio-action-inspector { grid-column: 1; grid-row: 3; }
  .pet-studio .studio-layout.is-behavior .studio-main { grid-column: 2; grid-row: 1 / span 2; }
}

@media (max-width: 700px) {
  .pet-studio .studio-layout,
  .pet-studio .studio-layout.is-behavior { display: flex; flex-direction: column; gap: 10px; }
  .pet-studio .studio-inspector { order: 1; width: 100%; }
  .pet-studio .studio-sidebar { order: 2; width: 100%; min-height: 0; }
  .pet-studio .studio-main { order: 3; width: 100%; }
  .pet-studio .studio-action-inspector { order: 4; width: 100%; }
  .pet-studio .action-list { display: flex; flex-direction: row; overflow-x: auto; }
  .pet-studio .action-row { min-width: 150px; flex: 0 0 150px; }
  .pet-studio .action-row__copy em { display: none; }
  .pet-studio .preview-stage { min-height: 0; }
  .pet-studio .studio-savebar { align-items: flex-start; flex-direction: column; }
  .pet-studio .studio-savebar__actions { width: 100%; justify-content: flex-start; }
}
/* OpenCowork 结构对齐：外层只分项目设置与编辑工作区，工作区内部再分 Actions 和 Timeline。 */
.pet-studio {
  max-width: 1440px;
  padding: 18px;
  background: transparent;
}

.pet-studio .studio-title-row { gap: 8px; }
.pet-studio .studio-title-icon { flex: 0 0 auto; color: var(--studio-accent); }
.pet-studio .studio-title-row h2 { font-size: 14px; letter-spacing: 0; }
.pet-studio .studio-inspector__header strong { display: inline-flex; align-items: center; gap: 6px; }

.pet-studio .studio-layout,
.pet-studio .studio-layout.is-behavior {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr) !important;
  align-items: start;
  gap: 16px;
}

.pet-studio .studio-inspector {
  grid-column: 1 !important;
  grid-row: 1 !important;
}

.pet-studio .studio-editor-pane {
  display: grid;
  grid-column: 2;
  grid-row: 1;
  grid-template-columns: 220px minmax(0, 1fr);
  align-items: start;
  gap: 16px;
  min-width: 0;
}

.pet-studio .studio-sidebar {
  position: static;
  grid-column: auto !important;
  grid-row: auto !important;
  min-height: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}

.pet-studio .sidebar-heading { min-height: 29px; padding: 0 0 8px; }
.pet-studio .sidebar-heading h3 { font-size: 12px; }
.pet-studio .sidebar-add-trigger { min-height: 28px; padding: 0 8px; font-size: 10px; }
.pet-studio .action-list { max-height: min(62vh, 42rem); margin: 0; padding-right: 3px; overflow-x: hidden; overflow-y: auto; }
.pet-studio .action-row { min-height: 58px; margin: 0 0 8px; border: 1px solid color-mix(in srgb, var(--studio-line) 70%, transparent); border-radius: 6px; background: color-mix(in srgb, var(--studio-surface) 78%, transparent); }
.pet-studio .action-row.is-selected { border-color: color-mix(in srgb, var(--studio-accent) 55%, var(--studio-line)); background: color-mix(in srgb, var(--studio-accent) 9%, var(--studio-surface)); box-shadow: 0 2px 8px color-mix(in srgb, var(--studio-accent) 8%, transparent); }
.pet-studio .action-row__select { min-height: 56px; gap: 8px; padding: 6px; }
.pet-studio .action-row__art { width: 48px; height: 48px; flex-basis: 48px; border-radius: 4px; }
.pet-studio .action-row__copy strong { font-size: 11px; }
.pet-studio .action-row__copy small,
.pet-studio .action-row__copy em { font-size: 9px; }
.pet-studio .sidebar-add { margin-top: 8px; padding: 10px 0 0; border-top: 1px solid var(--studio-line); }
.pet-studio .sidebar-footnote { display: none; }

.pet-studio .studio-editor-content {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 16px;
}

.pet-studio .studio-main {
  display: flex;
  min-width: 0;
  grid-column: auto !important;
  grid-row: auto !important;
  flex-direction: column;
  gap: 16px;
}

.pet-studio .studio-action-workspace {
  display: grid;
  grid-template-columns: minmax(220px, .75fr) minmax(0, 1.25fr);
  align-items: start;
  gap: 16px;
  min-width: 0;
}

.pet-studio .preview-panel {
  min-width: 0;
  min-height: 0;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
}
.pet-studio .preview-panel > .panel-heading { display: none; }
.pet-studio .preview-stage { width: 100%; min-height: 220px; aspect-ratio: 1; margin: 0; border-radius: 6px; }
.pet-studio .preview-stage img { max-width: 90%; max-height: 90%; filter: none; }

.pet-studio .studio-action-inspector {
  grid-column: auto !important;
  grid-row: auto !important;
  align-self: stretch;
  min-height: 0;
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.pet-studio .studio-action-inspector__header { padding: 0 0 10px; border-bottom: 0; }
.pet-studio .studio-action-inspector__body { gap: 12px; padding: 0; }
.pet-studio .studio-action-command { min-height: 32px; border-radius: 5px; }
.pet-studio .studio-action-name-field { margin-top: 1px; }
.pet-studio .studio-action-details { margin-top: 1px; }
.pet-studio .studio-action-details .field textarea { min-height: 54px; }
.pet-studio .studio-action-inspector__hint { font-size: 10px; }

.pet-studio .timeline-panel {
  grid-column: 1 / -1;
  min-height: 144px;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
}
.pet-studio .timeline-panel .panel-heading { min-height: 28px; }
.pet-studio .timeline-panel .panel-heading h3 { font-size: 12px; }
.pet-studio .timeline-panel .timeline-actions { gap: 6px; }
.pet-studio .frame-strip { min-height: 116px; margin-top: 8px; padding: 0 0 6px; }
.pet-studio .frame-card { width: 112px; height: 116px; flex-basis: 112px; padding: 6px; border-radius: 6px; }
.pet-studio .frame-card img { height: 82px; border-radius: 3px; }
.pet-studio .frame-card__duration { padding: 4px 0 0; font-size: 9px; }
.pet-studio .frame-card--add { gap: 7px; padding: 6px; }

.pet-studio .studio-savebar { margin-top: 0; }

@media (max-width: 980px) {
  .pet-studio .studio-layout,
  .pet-studio .studio-layout.is-behavior { grid-template-columns: 260px minmax(0, 1fr) !important; gap: 14px; }
  .pet-studio .studio-editor-pane { grid-template-columns: 200px minmax(0, 1fr); gap: 14px; }
  .pet-studio .studio-action-workspace { grid-template-columns: minmax(200px, .75fr) minmax(0, 1.25fr); gap: 12px; }
}

@media (max-width: 760px) {
  .pet-studio .studio-layout,
  .pet-studio .studio-layout.is-behavior { display: flex; flex-direction: column; gap: 12px; }
  .pet-studio .studio-inspector,
  .pet-studio .studio-editor-pane { width: 100%; }
  .pet-studio .studio-editor-pane { display: flex; flex-direction: column; gap: 14px; }
  .pet-studio .studio-sidebar { width: 100%; }
  .pet-studio .studio-main { width: 100%; }
  .pet-studio .studio-action-workspace { display: flex; flex-direction: column; gap: 14px; }
  .pet-studio .preview-stage { min-height: 0; }
  .pet-studio .studio-action-inspector { width: 100%; }
  .pet-studio .timeline-panel { width: 100%; }
}

/* 视口收口：外层只负责把工作台限制在窗口内，内部列表和编辑区各自滚动。 */
.pet-studio {
  box-sizing: border-box;
  /* PetSettings 自身左右各有 48px 内边距；抵消它后，工坊边界与顶部菜单统一。 */
  width: calc(100% + 96px);
  max-width: none;
  /* PetStudio 位于 PetSettings 的 tabs 下方，不能把宿主页面的整屏高度再占一遍。 */
  height: calc(100vh - 132px);
  max-height: calc(100vh - 132px);
  min-height: 0;
  margin: 0 -48px;
  padding: 14px 24px;
  overflow: hidden;
}

.pet-studio .studio-header {
  min-height: 64px;
  padding: 11px 14px;
  border: 1px solid var(--studio-line);
  border-radius: 8px;
  background: color-mix(in srgb, var(--studio-surface) 88%, transparent);
}

.pet-studio .studio-layout,
.pet-studio .studio-layout.is-behavior {
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr) !important;
  align-items: stretch;
  width: 100%;
  max-width: 100%;
  flex: 1;
  min-height: 0;
  min-width: 0;
  gap: 16px;
  overflow: hidden;
}

.pet-studio .studio-inspector {
  position: static;
  grid-column: 1 !important;
  grid-row: 1 !important;
  display: flex;
  width: auto;
  height: 100%;
  min-height: 0;
  max-height: none;
  flex-direction: column;
  overflow: hidden;
  border-radius: 8px;
  box-shadow: none;
  animation: none;
}

.pet-studio .studio-inspector__body {
  min-height: 0;
  flex: 1;
  overflow-x: hidden;
  overflow-y: auto;
}

.pet-studio .studio-inspector__body .inspector-section--meta-foot { display: none; }

.pet-studio .studio-editor-pane {
  grid-column: 2 !important;
  grid-row: 1 !important;
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  width: 100%;
  max-width: 100%;
  height: 100%;
  min-height: 0;
  min-width: 0;
  gap: 16px;
  overflow: hidden;
}

.pet-studio .studio-sidebar {
  height: 100%;
  min-height: 0;
  max-height: none;
  overflow: hidden;
}

.pet-studio .action-list {
  min-height: 0;
  max-height: none;
  flex: 1 1 auto;
  overflow-x: hidden;
  overflow-y: auto;
}

.pet-studio .studio-editor-content {
  display: flex;
  width: 100%;
  max-width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  gap: 10px;
  overflow: hidden;
}

.pet-studio .studio-main {
  display: flex;
  width: 100%;
  max-width: 100%;
  height: auto;
  min-width: 0;
  min-height: 0;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 10px;
  overflow-x: hidden;
  overflow-y: auto;
}

.pet-studio .studio-action-workspace {
  display: grid;
  width: 100%;
  max-width: 100%;
  grid-template-columns: minmax(0, 1fr) 188px;
  grid-template-rows: auto auto;
  align-items: start;
  gap: 10px 16px;
  min-width: 0;
  overflow-x: hidden;
}

/* grid/flex 子项默认会把内容的最小宽度带回父级；这里明确收缩边界，防止右侧操作面板把整列撑出视口。 */
.pet-studio .studio-layout > *,
.pet-studio .studio-editor-pane > *,
.pet-studio .studio-editor-content > *,
.pet-studio .studio-main > *,
.pet-studio .studio-action-workspace > * {
  min-width: 0;
  max-width: 100%;
}

.pet-studio .preview-panel {
  grid-column: 1;
  grid-row: 1;
  min-width: 0;
  min-height: 0;
  padding: 0;
  border: 0;
  background: transparent;
  box-shadow: none;
}

.pet-studio .preview-stage {
  width: 100%;
  min-height: 0;
  aspect-ratio: 1;
  margin: 0;
  border-radius: 8px;
}

.pet-studio .studio-action-inspector {
  grid-column: 2;
  grid-row: 1;
  width: 100%;
  max-width: 100%;
  min-height: 0;
  max-height: none;
  min-width: 0;
  overflow: visible;
}

.pet-studio .studio-action-inspector__header,
.pet-studio .studio-action-inspector__body,
.pet-studio .studio-action-inspector__title {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  overflow-x: hidden;
}

.pet-studio .studio-action-inspector__title span {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pet-studio .studio-action-inspector__body,
.pet-studio .studio-action-inspector__hint,
.pet-studio .studio-action-details,
.pet-studio .studio-action-commands,
.pet-studio .studio-action-command { min-width: 0; max-width: 100%; }

.pet-studio .timeline-panel {
  grid-column: 1 / -1;
  grid-row: 2;
  min-width: 0;
}

.pet-studio .studio-savebar {
  flex: 0 0 auto;
  margin-top: 0;
}

.pet-studio .studio-new-pet-tools {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 2px;
}

.pet-studio .studio-pose-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.pet-studio .studio-pose-tags button {
  min-height: 25px;
  border: 1px solid color-mix(in srgb, var(--studio-accent) 35%, var(--studio-line));
  border-radius: 999px;
  padding: 0 8px;
  background: transparent;
  color: var(--studio-muted);
  cursor: pointer;
  font: inherit;
  font-size: 9px;
  white-space: nowrap;
}

.pet-studio .studio-pose-tags button.is-active {
  background: color-mix(in srgb, var(--studio-accent) 11%, transparent);
  color: var(--studio-accent);
}

.pet-studio .studio-generate-pet-button { width: 100%; min-height: 34px; justify-content: center; }

@media (max-width: 760px) {
  .pet-studio {
    width: 100%;
    height: auto;
    max-height: none;
    margin: 0;
    padding: 12px;
    overflow: visible;
  }

  .pet-studio .studio-layout,
  .pet-studio .studio-layout.is-behavior {
    display: flex;
    height: auto;
    overflow: visible;
  }

  .pet-studio .studio-inspector,
  .pet-studio .studio-editor-pane,
  .pet-studio .studio-editor-content,
  .pet-studio .studio-main { height: auto; overflow: visible; }
  .pet-studio .studio-inspector { max-height: none; }
  .pet-studio .studio-editor-pane { display: flex; }
  .pet-studio .studio-main { flex: none; }
}

/* 动作编辑的预览只承担确认画面，不应该挤占整列高度；时间线和操作区才是主要工作区。 */
.pet-studio .studio-current-model {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 4px 8px;
  align-items: center;
  border: 1px solid var(--studio-line);
  border-radius: 7px;
  padding: 9px 10px;
  background: color-mix(in srgb, var(--studio-soft) 58%, transparent);
}
.pet-studio .studio-current-model > span { grid-column: 1 / -1; color: var(--studio-muted); font-size: 10px; }
.pet-studio .studio-current-model > strong { min-width: 0; overflow: hidden; color: var(--studio-ink); font-size: 11px; text-overflow: ellipsis; white-space: nowrap; }
.pet-studio .studio-current-model .studio-icon-button { grid-column: 2; grid-row: 2; width: 26px; min-height: 26px; }
.pet-studio .studio-settings-hint--error { color: var(--studio-red); }
.pet-studio .studio-main--actions { overflow: hidden; }
.pet-studio .preview-stage {
  width: min(100%, 420px);
  max-width: 420px;
  max-height: 420px;
  min-height: 0;
  margin: 0 auto;
}

/* 新增帧使用独立对话框，避免把长描述和数量控制塞进狭窄的右侧 ACTION 栏。 */
.studio-frame-modal-backdrop {
  --studio-ink: var(--settings-ink, #1f2933);
  --studio-muted: var(--settings-muted, #758091);
  --studio-line: color-mix(in srgb, var(--settings-line, #d8dee6) 82%, transparent);
  --studio-surface: var(--settings-surface, #ffffff);
  --studio-soft: color-mix(in srgb, var(--settings-strong-surface, #f4f6f8) 78%, transparent);
  --studio-accent: var(--mac-accent, #0a84ff);
  --studio-green: #2d966d;
  position: fixed;
  inset: 0;
  z-index: 1200;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(24, 31, 38, .52);
}
.studio-frame-modal {
  display: flex;
  width: min(100%, 448px);
  max-height: min(90vh, 560px);
  flex-direction: column;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--studio-line) 90%, #fff);
  border-radius: 10px;
  background: var(--studio-surface);
  box-shadow: 0 18px 50px rgba(25, 34, 42, .24);
  color: var(--studio-ink);
}
.studio-frame-modal__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 22px 24px 14px;
}
.studio-frame-modal__header h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 750;
  line-height: 1.25;
}
.studio-frame-modal__header p {
  margin: 7px 0 0;
  color: var(--studio-muted);
  font-size: 11px;
}
.studio-frame-modal__close {
  display: inline-grid;
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  place-items: center;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--studio-muted);
  cursor: pointer;
}
.studio-frame-modal__close:hover:not(:disabled) { background: var(--studio-soft); color: var(--studio-ink); }
.studio-frame-modal__body {
  display: flex;
  min-height: 0;
  flex-direction: column;
  gap: 16px;
  overflow: auto;
  padding: 0 24px 20px;
}
.studio-frame-modal__field {
  display: flex;
  flex-direction: column;
  gap: 7px;
  color: var(--studio-ink);
  font-size: 11px;
  font-weight: 700;
}
.studio-frame-modal__field textarea,
.studio-frame-modal__field input {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid var(--studio-line);
  border-radius: 7px;
  outline: 0;
  background: var(--studio-surface);
  color: var(--studio-ink);
  font: inherit;
  font-size: 12px;
}
.studio-frame-modal__field textarea {
  min-height: 64px;
  padding: 9px 10px;
  line-height: 1.45;
  resize: vertical;
}
.studio-frame-modal__field input { min-height: 36px; padding: 0 10px; }
.studio-frame-modal__field textarea:focus,
.studio-frame-modal__field input:focus {
  border-color: color-mix(in srgb, var(--studio-accent) 58%, var(--studio-line));
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--studio-accent) 12%, transparent);
}
.studio-frame-modal__field small {
  align-self: flex-end;
  margin-top: -3px;
  color: var(--studio-muted);
  font-size: 9px;
  font-weight: 400;
}
.studio-frame-modal__hint {
  margin: -2px 0 0;
  color: var(--studio-muted);
  font-size: 10px;
  line-height: 1.5;
}
.studio-frame-modal__error {
  margin: 0;
  color: #b84d4d;
  font-size: 11px;
  line-height: 1.5;
}
.studio-frame-modal__footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 24px 22px;
  border-top: 1px solid var(--studio-line);
}
.studio-frame-modal__footer-spacer { flex: 1; }
.studio-frame-modal__footer .studio-command,
.studio-frame-modal__footer .studio-subtle-button {
  display: inline-flex;
  min-height: 36px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid var(--studio-line);
  border-radius: 7px;
  padding: 0 12px;
  background: transparent;
  color: var(--studio-ink);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}
.studio-frame-modal__footer .studio-command {
  display: inline-flex;
  min-height: 36px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1px solid var(--studio-green);
  border-radius: 7px;
  padding: 0 13px;
  background: var(--studio-green);
  color: #fff;
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}
.studio-frame-modal__footer .studio-command:disabled,
.studio-frame-modal__footer .studio-subtle-button:disabled { cursor: not-allowed; opacity: .45; }
.studio-frame-modal__footer .studio-command:hover:not(:disabled),
.studio-frame-modal__footer .studio-subtle-button:hover:not(:disabled) { filter: brightness(.98); }

.studio-frame-modal-enter-active,
.studio-frame-modal-leave-active { transition: opacity .16s ease; }
.studio-frame-modal-enter-active .studio-frame-modal,
.studio-frame-modal-leave-active .studio-frame-modal { transition: transform .16s ease, opacity .16s ease; }
.studio-frame-modal-enter-from,
.studio-frame-modal-leave-to { opacity: 0; }
.studio-frame-modal-enter-from .studio-frame-modal,
.studio-frame-modal-leave-to .studio-frame-modal { transform: translateY(8px); opacity: 0; }

@media (max-width: 760px) {
  .pet-studio .studio-main--actions { overflow: visible; }
  .pet-studio .preview-stage { width: min(100%, 360px); max-width: 360px; max-height: 360px; }
  .studio-frame-modal-backdrop { padding: 12px; }
  .studio-frame-modal__header { padding: 18px 18px 12px; }
  .studio-frame-modal__body { padding: 0 18px 16px; }
  .studio-frame-modal__footer { flex-wrap: wrap; padding: 12px 18px 18px; }
  .studio-frame-modal__footer-spacer { display: none; }
  .studio-frame-modal__footer .studio-frame-modal__footer-spacer + .studio-subtle-button { margin-left: auto; }
}
</style>
