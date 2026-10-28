import { WeatherResponse } from '../../weather/weather.models';

export interface WallSummaryOptions {
  show_points: boolean;
  show_weather: boolean;
  show_tomorrow: boolean;
  show_pictures: boolean;
  show_greeting: boolean;
  night_mode: boolean;
  show_bonus_banner: boolean;
}

export type WallTaskState = 'overdue' | 'pending' | 'done';

export interface WallSummaryTask {
  occurrence_id: string;
  title: string;
  due_at: string;
  state: WallTaskState;
  completed_at?: string;
}

export interface WallSummaryChild {
  member_id: string;
  name: string;
  colour: string;
  avatar_id: string | null;
  points: number | null;
  tasks: WallSummaryTask[];
}

export interface WallSummaryAttendee {
  member_id: string;
  name: string;
  colour: string;
  avatar_id: string | null;
}

export interface WallSummaryEvent {
  title: string;
  date: string;
  starts_at: string;
  ends_at: string;
  all_day: boolean;
  location: string | null;
  /** Empty when the event is for everyone. */
  attendees: WallSummaryAttendee[];
  /** Birthdays carry the member's name as the title, and the age they turn. */
  kind?: 'event' | 'birthday';
  age?: number;
}

export type WallBonusClaimMode = 'first_come' | 'everyone_once';

export interface WallSummaryBonus {
  id: string;
  title: string;
  points: number;
  claim_mode: WallBonusClaimMode;
  ends_at: string;
}

export interface WallSummary {
  family_name: string;
  date: string;
  timezone: string;
  now: string;
  options: WallSummaryOptions;
  time_format?: '12h' | '24h';
  children: WallSummaryChild[];
  events: WallSummaryEvent[];
  bonus_tasks: WallSummaryBonus[];
  weather: WeatherResponse | null;
}
