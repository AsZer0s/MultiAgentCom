import { ref, computed } from 'vue'
import en from './locales/en'
import zh from './locales/zh'

export type Locale = 'en' | 'zh'

const locales: Record<Locale, typeof en> = { en, zh }

const currentLocale = ref<Locale>((localStorage.getItem('locale') as Locale) || 'zh')

function setLocale(locale: Locale) {
  currentLocale.value = locale
  localStorage.setItem('locale', locale)
  document.documentElement.lang = locale
}

function t(key: string, params?: Record<string, string | number>): string {
  const keys = key.split('.')
  let value: any = locales[currentLocale.value]

  for (const k of keys) {
    if (value && typeof value === 'object' && k in value) {
      value = value[k]
    } else {
      // Fallback to English
      let fallback: any = en
      for (const fk of keys) {
        if (fallback && typeof fallback === 'object' && fk in fallback) {
          fallback = fallback[fk]
        } else {
          return key // Return key if not found
        }
      }
      value = fallback
      break
    }
  }

  if (typeof value !== 'string') {
    return key
  }

  // Replace parameters
  if (params) {
    Object.entries(params).forEach(([k, v]) => {
      value = value.replace(`{${k}}`, String(v))
    })
  }

  return value
}

export function useI18n() {
  return {
    locale: computed(() => currentLocale.value),
    t,
    setLocale,
    availableLocales: ['en', 'zh'] as Locale[],
  }
}
