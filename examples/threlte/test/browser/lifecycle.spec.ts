import { expect, test } from '@playwright/test';

test('one real session survives scene lifecycles, commands, events, and independent mounts', async ({ page }) => {
  const errors: string[] = [];
  const sockets: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
  page.on('websocket', (socket) => sockets.push(socket.url()));

  await page.goto('/');
  await expect(page.getByTestId('connection-status')).toHaveText('connected');
  await expect(page.getByRole('button', { name: 'Send pulse' })).toBeEnabled();
  await expect(page.locator('canvas')).toHaveCount(1);
  const session = await page.getByTestId('session-id').innerText();
  const beforePulse = Number(await page.getByTestId('pulse-count').innerText());
  const beforeWorld = Number(await page.getByTestId('world-pulse-count').innerText());
  const beforeEvents = Number(await page.getByTestId('event-count').innerText());

  await page.getByRole('button', { name: 'Send pulse' }).click();
  await expect(page.getByTestId('pulse-count')).toHaveText(String(beforePulse + 1));
  await expect.poll(async () => Number(await page.getByTestId('world-pulse-count').innerText())).toBeGreaterThan(beforeWorld);
  await expect.poll(async () => Number(await page.getByTestId('event-count').innerText())).toBeGreaterThan(beforeEvents);

  const uptime = parseInt(await page.getByTestId('uptime').innerText(), 10);
  await page.getByRole('button', { name: 'Unmount scene' }).click();
  await expect(page.locator('canvas')).toHaveCount(0);
  await expect(page.getByTestId('session-id')).toHaveText(session);
  await expect(page.getByTestId('connection-status')).toHaveText('connected');
  await expect.poll(async () => parseInt(await page.getByTestId('uptime').innerText(), 10)).toBeGreaterThan(uptime);
  await page.getByRole('button', { name: 'Send pulse' }).click();
  await expect(page.getByTestId('pulse-count')).toHaveText(String(beforePulse + 2));

  await page.getByRole('button', { name: 'Remount scene' }).first().click();
  await expect(page.locator('canvas')).toHaveCount(1);
  await expect(page.getByTestId('remount-count')).toHaveText('1 remount');
  await expect(page.getByTestId('session-id')).toHaveText(session);

  const restore = page.getByRole('button', { name: 'Restore beacon' });
  if (await restore.isVisible()) await restore.click();
  await page.getByRole('button', { name: 'Remove beacon' }).click();
  await expect(restore).toBeVisible();
  await restore.click();
  await expect(page.getByRole('button', { name: 'Remove beacon' })).toBeVisible();

  await page.getByRole('checkbox', { name: 'Second, filtered scene' }).check();
  await expect(page.locator('canvas')).toHaveCount(2);
  await page.getByRole('checkbox', { name: 'Application transform control' }).check();
  await page.getByRole('button', { name: 'Teleport actor' }).click();
  await page.getByRole('checkbox', { name: 'Application transform control' }).uncheck();
  await page.getByRole('button', { name: 'Teleport actor' }).click();
  await page.getByRole('checkbox', { name: 'Second, filtered scene' }).uncheck();

  for (let count = 2; count <= 3; count++) {
    await page.getByRole('button', { name: 'Unmount scene' }).click();
    await expect(page.locator('canvas')).toHaveCount(0);
    await page.getByRole('button', { name: 'Remount scene' }).first().click();
    await expect(page.locator('canvas')).toHaveCount(1);
    await expect(page.getByTestId('remount-count')).toHaveText(`${count} remounts`);
  }

  const finalEvents = Number(await page.getByTestId('event-count').innerText());
  await page.getByRole('button', { name: 'Send pulse' }).click();
  await expect(page.getByTestId('event-count')).toHaveText(String(finalEvents + 1));
  await expect(page.getByTestId('pulse-count')).toHaveText(String(beforePulse + 3));
  await expect(page.getByTestId('session-id')).toHaveText(session);
  expect(sockets).toHaveLength(1);
  expect(errors).toEqual([]);
});
