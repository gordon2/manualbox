import { useCallback, useEffect, useState } from "react";

import { api, ApiError } from "../api/client";
import type { Device } from "../api/types";
import { Alert, Button, Card, Field } from "../ui";

/** The inventory: everything the household owns, and the way in to each one. */
export function Devices({ onOpen }: { onOpen: (device: Device) => void }) {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const reload = useCallback(async () => {
    try {
      const { devices } = await api.devices();
      setDevices(devices);
      setError(null);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "Could not load your devices.");
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  return (
    <section>
      <div className="flex items-baseline justify-between gap-4">
        <h2 className="font-display text-lg text-ink">Devices</h2>
        {!adding ? (
          <Button variant="quiet" onClick={() => setAdding(true)}>
            Add a device
          </Button>
        ) : null}
      </div>

      {error ? (
        <div className="mt-3">
          <Alert>{error}</Alert>
        </div>
      ) : null}

      {adding ? (
        <AddDevice
          onCancel={() => setAdding(false)}
          onAdded={(device) => {
            setAdding(false);
            void reload();
            onOpen(device);
          }}
        />
      ) : null}

      {devices === null ? (
        <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">Loading…</Card>
      ) : devices.length === 0 ? (
        <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">
          Nothing yet. Add a device, then upload its manual.
        </Card>
      ) : (
        <ul className="mt-3 space-y-2">
          {devices.map((device) => (
            <li key={device.id}>
              <Card className="px-4 py-3">
                <button
                  className="flex w-full items-center gap-3 text-left"
                  onClick={() => onOpen(device)}
                >
                  <span className="text-sm font-medium text-ink">{device.name}</span>
                  {device.brand || device.model ? (
                    <span className="truncate text-xs text-ink-faint">
                      {[device.brand, device.model].filter(Boolean).join(" ")}
                    </span>
                  ) : null}
                  <span className="ml-auto text-xs text-accent">Open</span>
                </button>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function AddDevice({
  onAdded,
  onCancel,
}: {
  onAdded: (device: Device) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [brand, setBrand] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      onAdded(
        await api.createDevice({ name: name.trim(), brand: brand.trim(), model: model.trim() }),
      );
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "Could not add the device.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="mt-3 p-4">
      <form className="space-y-3" onSubmit={submit}>
        <Field
          label="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Dishwasher"
          autoFocus
          required
        />
        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            label="Brand"
            value={brand}
            onChange={(e) => setBrand(e.target.value)}
            placeholder="Bosch"
          />
          <Field
            label="Model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="SMS4HVW33E"
          />
        </div>
        {/* Serial number and price are deliberately absent: they are encrypted
            fields and the keyring is not wired into the schema yet. */}
        {error ? <Alert>{error}</Alert> : null}
        <div className="flex gap-2">
          <Button type="submit" disabled={busy || name.trim() === ""}>
            {busy ? "Adding…" : "Add device"}
          </Button>
          <Button type="button" variant="quiet" onClick={onCancel}>
            Cancel
          </Button>
        </div>
      </form>
    </Card>
  );
}
