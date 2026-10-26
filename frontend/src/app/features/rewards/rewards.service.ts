import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiClient } from '../../core/api/api-client.service';
import {
  ManualPointsRequest,
  MemberBalancesResponse,
  MyPointsResponse,
  PointsChangeResult,
  PointsEntryPage,
  PointsEntryQuery,
  RedeemResult,
  RedemptionPage,
  RedemptionQuery,
  Reward,
  RewardInput,
  RewardList,
  RewardSettings,
} from './rewards.models';

@Injectable({ providedIn: 'root' })
export class RewardsService {
  private api = inject(ApiClient);

  /** Rewards the signed-in user may see. A child sees only active rewards that name them. */
  list(): Observable<RewardList> {
    return this.api.get<RewardList>('/api/v1/rewards');
  }

  /** The signed-in user's balance, reserved points and this week's earnings. */
  myPoints(): Observable<MyPointsResponse> {
    return this.api.get<MyPointsResponse>('/api/v1/me/points');
  }

  /** Each active child's balance and reserved points. Parents only. */
  memberBalances(): Observable<MemberBalancesResponse> {
    return this.api.get<MemberBalancesResponse>('/api/v1/points/balances');
  }

  /** The most recent points entries, newest first. A child sees only their own. */
  entries(limit = 10): Observable<PointsEntryPage> {
    return this.api.get<PointsEntryPage>(`/api/v1/points/entries?limit=${limit}`);
  }

  /** Points entries with optional member, type and cursor filters, newest first. */
  entryPage(query: PointsEntryQuery): Observable<PointsEntryPage> {
    const params = new URLSearchParams();
    if (query.member_id) params.set('member_id', query.member_id);
    if (query.type) params.set('type', query.type);
    if (query.cursor) params.set('cursor', query.cursor);
    params.set('limit', String(query.limit ?? 50));
    return this.api.get<PointsEntryPage>(`/api/v1/points/entries?${params.toString()}`);
  }

  /** Redemption requests filtered by status, with cursor paging. A parent sees every member's requests. */
  redemptionPage(query: RedemptionQuery): Observable<RedemptionPage> {
    const params = new URLSearchParams();
    if (query.status) params.set('status', query.status);
    if (query.member_id) params.set('member_id', query.member_id);
    if (query.cursor) params.set('cursor', query.cursor);
    params.set('limit', String(query.limit ?? 20));
    return this.api.get<RedemptionPage>(`/api/v1/redemptions?${params.toString()}`);
  }

  /** Approves a pending request. version is the request version the approval is based on. */
  approveRedemption(redemptionId: string, version: number): Observable<RedeemResult> {
    return this.api.post<RedeemResult>(`/api/v1/redemptions/${redemptionId}/approve`, {}, version);
  }

  /** Declines a pending request with a reason. The points go back to the requester. */
  rejectRedemption(redemptionId: string, reason: string, version: number): Observable<RedeemResult> {
    return this.api.post<RedeemResult>(`/api/v1/redemptions/${redemptionId}/reject`, { reason }, version);
  }

  /**
   * Asks for a reward. idempotencyKey must be new for each request and reused only when retrying
   * the same request, so a double tap cannot hold points twice.
   */
  redeem(rewardId: string, idempotencyKey: string): Observable<RedeemResult> {
    return this.api.post<RedeemResult>(`/api/v1/rewards/${rewardId}/redeem`, {}, undefined, {
      'Idempotency-Key': idempotencyKey,
    });
  }

  /** Cancels a pending request of your own. version is the request version the cancel is based on. */
  cancel(redemptionId: string, version: number): Observable<RedeemResult> {
    return this.api.post<RedeemResult>(`/api/v1/redemptions/${redemptionId}/cancel`, {}, version);
  }

  /** The family defaults for new rewards. Parents only. */
  settings(): Observable<RewardSettings> {
    return this.api.get<RewardSettings>('/api/v1/rewards/settings');
  }

  /** Replaces the family defaults for new rewards. Parents only. */
  saveSettings(input: RewardSettings): Observable<RewardSettings> {
    return this.api.put<RewardSettings>('/api/v1/rewards/settings', input);
  }

  /** Adds a reward to the catalogue. Parents only. */
  create(input: RewardInput): Observable<Reward> {
    return this.api.post<Reward>('/api/v1/rewards', input);
  }

  /** Replaces the editable fields of a reward. version is the version the edit is based on. */
  update(rewardId: string, input: RewardInput, version: number): Observable<Reward> {
    return this.api.patch<Reward>(`/api/v1/rewards/${rewardId}`, input, version);
  }

  /** Archives a reward. Past requests keep their history. version is the version the archive is based on. */
  archive(rewardId: string, version: number): Observable<unknown> {
    return this.api.delete<unknown>(`/api/v1/rewards/${rewardId}`, version);
  }

  /**
   * Gives or takes points for a child. idempotencyKey must be new for each change and reused only
   * when retrying the same change.
   */
  postPoints(memberId: string, input: ManualPointsRequest, idempotencyKey: string): Observable<PointsChangeResult> {
    return this.api.post<PointsChangeResult>(`/api/v1/family/members/${memberId}/points`, input, undefined, {
      'Idempotency-Key': idempotencyKey,
    });
  }

  /** Undoes a penalty once, with a reason. */
  reversePenalty(entryId: string, reason: string): Observable<PointsChangeResult> {
    return this.api.post<PointsChangeResult>(`/api/v1/points/entries/${entryId}/reverse`, { reason });
  }
}
