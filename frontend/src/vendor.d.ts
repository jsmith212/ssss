// signalsmith-stretch ships no type declarations. This covers the parts of
// its README API the audio engine uses.
declare module "signalsmith-stretch" {
  export interface StretchSchedule {
    /** Audio context time (seconds) at which the change applies. */
    output?: number;
    active?: boolean;
    /** Position in the input buffer, seconds. */
    input?: number;
    rate?: number;
    semitones?: number;
    tonalityHz?: number;
    formantSemitones?: number;
    formantCompensation?: boolean;
    formantBaseHz?: number;
    loopStart?: number;
    loopEnd?: number;
  }

  export interface StretchNode extends AudioWorkletNode {
    inputTime: number;
    setUpdateInterval(seconds: number, callback?: (inputTime: number) => void): void;
    schedule(change: StretchSchedule): void;
    start(when?: number, offset?: number, duration?: number, rate?: number, semitones?: number): void;
    stop(when?: number): void;
    addBuffers(channels: Float32Array[]): Promise<number>;
    dropBuffers(toSeconds?: number): Promise<{ start: number; end: number }>;
    latency(): number;
    configure(options: {
      blockMs?: number | null;
      intervalMs?: number;
      splitComputation?: boolean;
      preset?: "default" | "cheaper";
    }): void;
  }

  export default function SignalsmithStretch(
    context: BaseAudioContext,
    channelOptions?: AudioWorkletNodeOptions,
  ): Promise<StretchNode>;
}
