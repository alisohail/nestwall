import { Routes } from '@angular/router';
import { authGuard } from './core/auth/auth.guard';
import { anyPermissionGuard } from './core/auth/permission.guard';
import { childHomeGuard } from './core/auth/child-home.guard';
import { setupGuard } from './core/setup/setup.guard';
import { ShellComponent } from './core/shell/shell.component';
import { unsavedChangesGuard } from './core/unsaved/unsaved-changes';

export const routes: Routes = [
  {
    path: 'setup',
    canActivate: [setupGuard],
    loadComponent: () => import('./features/setup/setup.component').then(m => m.SetupComponent)
  },
  {
    path: 'login',
    loadComponent: () => import('./features/login/login.component').then(m => m.LoginComponent)
  },
  {
    path: 'change-password',
    canActivate: [authGuard],
    loadComponent: () => import('./features/login/change-password.component').then(m => m.ChangePasswordComponent)
  },
  {
    path: 'reset-password/:token',
    loadComponent: () => import('./features/login/reset-password.component').then(m => m.ResetPasswordComponent)
  },
  {
    path: 'pair',
    loadComponent: () => import('./features/wall/pair/wall-pair.component').then(m => m.WallPairComponent)
  },
  {
    path: 'wall',
    loadComponent: () => import('./features/wall/display/wall-display.component').then(m => m.WallDisplayComponent)
  },
  {
    path: '',
    component: ShellComponent,
    canActivate: [authGuard],
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'dashboard' },
      { path: 'dashboard', canActivate: [childHomeGuard], loadComponent: () => import('./features/dashboard/dashboard.component').then(m => m.DashboardComponent) },
      { path: 'today', loadComponent: () => import('./features/today/today.component').then(m => m.TodayComponent) },
      { path: 'my-tasks', loadComponent: () => import('./features/my-tasks/my-tasks.component').then(m => m.MyTasksComponent) },
      { path: 'my-tasks/:id', loadComponent: () => import('./features/my-tasks/my-task/my-task.component').then(m => m.MyTaskComponent) },
      { path: 'my-tasks/bonus/:id', loadComponent: () => import('./features/bonus/kid/kid-bonus-detail.component').then(m => m.KidBonusDetailComponent) },
      { path: 'tasks', loadComponent: () => import('./features/tasks/tasks.component').then(m => m.TasksComponent) },
      { path: 'tasks/new', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/tasks/task-form/task-form.component').then(m => m.TaskFormComponent) },
      { path: 'tasks/:id/edit', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/tasks/task-form/task-form.component').then(m => m.TaskFormComponent) },
      { path: 'bonus', canActivate: [anyPermissionGuard('bonus.manage')], data: { titleKey: 'shell.nav.bonus' }, loadComponent: () => import('./features/bonus/bonus.component').then(m => m.BonusComponent) },
      { path: 'bonus/new', canActivate: [anyPermissionGuard('bonus.manage')], canDeactivate: [unsavedChangesGuard], data: { titleKey: 'shell.nav.bonus' }, loadComponent: () => import('./features/bonus/bonus-new/bonus-new.component').then(m => m.BonusNewComponent) },
      { path: 'approvals', canActivate: [anyPermissionGuard('tasks.approve', 'rewards.approve')], data: { titleKey: 'shell.nav.approvals' }, loadComponent: () => import('./features/approvals/approvals.component').then(m => m.ApprovalsComponent) },
      { path: 'calendar', loadComponent: () => import('./features/calendar/calendar.component').then(m => m.CalendarComponent) },
      { path: 'rewards', loadComponent: () => import('./features/rewards/rewards.component').then(m => m.RewardsComponent) },
      { path: 'rewards/requests', loadComponent: () => import('./features/rewards/requests/reward-requests.component').then(m => m.RewardRequestsComponent) },
      { path: 'points', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/points/points.component').then(m => m.PointsComponent) },
      { path: 'share', loadComponent: () => import('./features/sharing/share/share.component').then(m => m.ShareComponent) },
      { path: 'family/members/:id', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/family/member-edit/member-edit.component').then(m => m.MemberEditComponent) },
      { path: 'family', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/family/family.component').then(m => m.FamilyComponent) },
      { path: 'settings', loadComponent: () => import('./features/settings/settings-hub/settings-hub.component').then(m => m.SettingsHubComponent) },
      { path: 'settings/admin', loadComponent: () => import('./features/settings/settings.component').then(m => m.SettingsComponent), children: [
          { path: '', pathMatch: 'full', redirectTo: 'templates' },
          {
            path: 'templates',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/templates/task-templates-settings.component').then(m => m.TaskTemplatesSettingsComponent),
          },
          {
            path: 'bonus-templates',
            data: { titleKey: 'settings.nav.bonus' },
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/bonus-templates/bonus-templates-settings.component').then(m => m.BonusTemplatesSettingsComponent),
          },
          {
            path: 'categories',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/categories/task-categories-settings.component').then(m => m.TaskCategoriesSettingsComponent),
          },
          {
            path: 'points',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/points/points-settings.component').then(m => m.PointsSettingsComponent),
          },
          {
            path: 'badges',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/badges/badges-settings.component').then(m => m.BadgesSettingsComponent),
          },
          {
            path: 'calendars',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/calendars/calendars-settings.component').then(m => m.CalendarsSettingsComponent),
          },
          {
            path: 'notifications',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/notifications/notifications-settings.component').then(m => m.NotificationsSettingsComponent),
          },
          {
            path: 'email',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/email/email-settings.component').then(m => m.EmailSettingsComponent),
          },
          {
            path: 'wall',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/wall-devices/wall-devices-settings.component').then(m => m.WallDevicesSettingsComponent),
          },
          {
            path: 'integrations',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/integrations/integrations-settings.component').then(m => m.IntegrationsSettingsComponent),
          },
          {
            path: 'data',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/data/data-settings.component').then(m => m.DataSettingsComponent),
          },
          {
            path: 'diagnostics',
            loadComponent: () => import('./features/settings/diagnostics/diagnostics.component').then(m => m.DiagnosticsComponent),
          },
          {
            path: 'general',
            canDeactivate: [unsavedChangesGuard],
            loadComponent: () => import('./features/settings/general/general-settings.component').then(m => m.GeneralSettingsComponent),
          },
        ],
      },
      { path: 'me', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/me/me.component').then(m => m.MeComponent) },
      { path: 'profile', canDeactivate: [unsavedChangesGuard], loadComponent: () => import('./features/profile/profile.component').then(m => m.ProfileComponent) },
      { path: 'me/badges', loadComponent: () => import('./features/badges/my-badges/my-badges.component').then(m => m.MyBadgesComponent) }
    ]
  },
  {
    path: 'forbidden',
    loadComponent: () => import('./features/errors/forbidden/forbidden.component').then(m => m.ForbiddenComponent)
  },
  {
    path: 'server-error',
    loadComponent: () => import('./features/errors/server-error/server-error.component').then(m => m.ServerErrorComponent)
  },
  {
    path: '**',
    loadComponent: () => import('./features/errors/not-found/not-found.component').then(m => m.NotFoundComponent)
  }
];
