import { TranslocoTestingModule, TranslocoTestingOptions } from '@ngneat/transloco';
import en from '../../../assets/i18n/en.json';

export function getTranslocoTestingModule(options: TranslocoTestingOptions = {}) {
  return TranslocoTestingModule.forRoot({
    langs: {
      en,
    },
    translocoConfig: {
      availableLangs: ['en', 'en-XA'],
      defaultLang: 'en',
    },
    preloadLangs: false,
    ...options,
  });
}
