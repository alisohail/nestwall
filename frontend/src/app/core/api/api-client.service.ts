import { Injectable, inject } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, catchError, defer, finalize, from, switchMap, tap, throwError } from 'rxjs';
import { BusyService } from '../busy/busy.service';

export interface ApiError {
  code: string;
  status?: number;
  params?: Record<string, string>;
  message?: string;
}

/** silent writes are background updates, such as marking a notification read. They do not show the saving overlay. */
export interface WriteOptions {
  silent?: boolean;
}

@Injectable({
  providedIn: 'root',
})
export class ApiClient {
  private http = inject(HttpClient);
  private busy = inject(BusyService);
  private csrfToken: string | null = null;

  setCsrfToken(token: string): void {
    this.csrfToken = token;
  }

  getCsrfToken(): string | null {
    return this.csrfToken;
  }

  get<T>(url: string): Observable<T> {
    return this.http.get<T>(url).pipe(catchError(this.handleError));
  }

  /** Fetches a file, for example an ICS export. The response body is returned as a Blob. */
  download(url: string): Observable<Blob> {
    return this.http.get(url, { responseType: 'blob' }).pipe(catchError(this.handleError));
  }

  /**
   * POSTs a body and returns the file in the response. Errors are JSON even though the
   * request asked for a Blob, so the error body is read back as text first.
   */
  postDownload(url: string, body: any): Observable<Blob> {
    const request = this.http.post(url, body, { responseType: 'blob' }).pipe(
      catchError((error: HttpErrorResponse) => {
        if (!(error.error instanceof Blob)) return this.handleError(error);
        return from(error.error.text()).pipe(
          switchMap((text) =>
            this.handleError(new HttpErrorResponse({ error: text, status: error.status, url: error.url ?? undefined })),
          ),
        );
      }),
    );
    return this.tracked(request);
  }

  /**
   * ifMatch, when given, is sent as the If-Match header so the server can reject stale writes.
   * extraHeaders adds further headers, for example Idempotency-Key.
   */
  post<T>(
    url: string,
    body: any,
    ifMatch?: number,
    extraHeaders?: Record<string, string>,
    writeOptions?: WriteOptions,
  ): Observable<T> {
    const headers: Record<string, string> = { ...(extraHeaders ?? {}) };
    if (ifMatch !== undefined) headers['If-Match'] = `"${ifMatch}"`;
    const options = Object.keys(headers).length === 0 ? {} : { headers };
    return this.tracked(this.http.post<T>(url, body, options).pipe(catchError(this.handleError)), writeOptions);
  }

  /** ifMatch, when given, is sent as the If-Match header so the server can reject stale writes. */
  put<T>(url: string, body: any, ifMatch?: number, writeOptions?: WriteOptions): Observable<T> {
    const options = ifMatch === undefined ? {} : { headers: { 'If-Match': `"${ifMatch}"` } };
    return this.tracked(this.http.put<T>(url, body, options).pipe(catchError(this.handleError)), writeOptions);
  }

  /** Partial update. ifMatch, when given, is sent as the If-Match header. */
  patch<T>(url: string, body: any, ifMatch?: number, writeOptions?: WriteOptions): Observable<T> {
    const options = ifMatch === undefined ? {} : { headers: { 'If-Match': `"${ifMatch}"` } };
    return this.tracked(this.http.patch<T>(url, body, options).pipe(catchError(this.handleError)), writeOptions);
  }

  delete<T>(url: string, ifMatch?: number, writeOptions?: WriteOptions): Observable<T> {
    const options = ifMatch === undefined ? {} : { headers: { 'If-Match': `"${ifMatch}"` } };
    return this.tracked(this.http.delete<T>(url, options).pipe(catchError(this.handleError)), writeOptions);
  }

  /** Shows the saving overlay while a write runs. Failures scroll to the error message. */
  private tracked<T>(request: Observable<T>, writeOptions?: WriteOptions): Observable<T> {
    if (writeOptions?.silent) return request;
    return defer(() => {
      this.busy.begin();
      let failed = false;
      return request.pipe(
        tap({ error: () => (failed = true) }),
        finalize(() => this.busy.end(failed)),
      );
    });
  }

  private handleError(error: HttpErrorResponse): Observable<never> {
    let apiError: ApiError = { code: 'unknown' };
    if (error.error instanceof ErrorEvent) {
      apiError = { code: 'client_error', message: error.error.message };
    } else if (error.error && typeof error.error === 'object') {
      // The backend sends { error: { code, message, params } }. Older shapes are flat.
      const nested = error.error.error && typeof error.error.error === 'object' ? error.error.error : null;
      const detail = nested ?? error.error;
      const flatMessage = typeof error.error.error === 'string' ? error.error.error : undefined;
      apiError = {
        code: detail.code || flatMessage || 'server_error',
        params: detail.params,
        message: detail.message || flatMessage || error.message,
      };
    } else if (typeof error.error === 'string' && error.error.startsWith('{')) {
      try {
        const parsed = JSON.parse(error.error);
        apiError = {
          code: parsed.code || parsed.error || 'server_error',
          params: parsed.params,
          message: parsed.message || parsed.error || error.message,
        };
      } catch {
        apiError = { code: 'server_error', message: error.error };
      }
    } else {
      apiError = { code: 'server_error', message: error.message };
    }
    return throwError(() => ({ ...apiError, status: error.status }));
  }
}
