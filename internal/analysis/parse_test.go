package analysis

import (
	"slices"
	"testing"
)

func TestParseName(t *testing.T) {
	type want struct {
		bpm  float64
		key  string
		weak bool
		typ  string
		tags []string
	}
	tests := []struct {
		path string
		want want
	}{
		// --- Splice-style: LABEL_pack_type_name_bpm_key ---
		{"KSHMR_Bass_Loop_01_128_Am.wav", want{128, "Am", false, "loop", []string{"bass"}}},
		{"OS_TR_140_Fmin_Melody_Loop_Dreams.wav", want{140, "Fm", false, "loop", nil}},
		{"SO_GH_95_drum_loop_dusty.wav", want{95, "", false, "loop", []string{"drums"}}},
		{"PL_TH_Kick_01.wav", want{0, "", false, "", []string{"drums/kick"}}},
		{"DS_VEH_124_synth_loop_arpy_Gmin.wav", want{124, "Gm", false, "loop", []string{"synth"}}},
		{"SC_LFH_90_piano_loop_dusty_keys_Dmaj.wav", want{90, "D", false, "loop", []string{"keys/piano"}}},
		{"ESM_Deep_House_Bass_Loop_122_BPM_F#m.wav", want{122, "F#m", false, "loop", []string{"bass"}}},
		{"blk_tt_kick_one_shot_punchy.wav", want{0, "", false, "oneshot", []string{"drums/kick"}}},
		{"SPLICE_snare_tight_03.wav", want{0, "", false, "", []string{"drums/snare"}}},
		{"RU_DD_808_long_C.wav", want{0, "C", false, "", []string{"bass/808"}}},
		{"TL_vocal_chop_loop_100_Bbm.wav", want{100, "A#m", false, "loop", []string{"vocals"}}},
		{"OLIVER_clap_stack_02.wav", want{0, "", false, "", []string{"drums/clap"}}},
		{"SM_White_Label_hihat_loop_126.wav", want{126, "", false, "loop", []string{"drums/hihat"}}},
		{"VIRTUAL_RIOT_riser_8_bars_150.wav", want{150, "", false, "", []string{"fx/riser"}}},
		{"prime_loops_guitar_riff_85_Em.wav", want{85, "Em", false, "loop", []string{"guitar"}}},
		{"FL_LSD_perc_loop_shaker_110.wav", want{110, "", false, "loop", []string{"drums/percussion"}}},
		{"OS_SJ_Fill_Loop_07_130.wav", want{130, "", false, "loop", []string{"drums"}}},
		{"JUNGLE_break_170_amen_chopped.wav", want{170, "", false, "", []string{"drums"}}},
		{"sample_magic_pad_one_shot_warm_C#.wav", want{0, "C#", false, "oneshot", []string{"synth/pad"}}},
		{"HY_BRASS_stab_Gm.wav", want{0, "Gm", false, "oneshot", []string{"orchestral/brass", "synth/stab"}}},

		// --- Loopmasters-style: Name_Key_BPM, folders carry context ---
		{"Bass Loops/DTH_Bass_Loop_A_125.wav", want{125, "A", false, "loop", []string{"bass"}}},
		{"Loops/Drum_Loops/LM_Drm_125_Full_07.wav", want{125, "", false, "loop", []string{"drums"}}},
		{"125bpm/Music Loops/SynthLoop_Cm_03.wav", want{125, "Cm", false, "loop", []string{"synth"}}},
		{"One Shots/Drum Hits/Kicks/Kick_Deep_04.wav", want{0, "", false, "oneshot", []string{"drums/kick"}}},
		{"LOOPS/TOP_LOOPS/Top_Loop_128_12.wav", want{128, "", false, "loop", []string{"drums"}}},
		{"Loops/Synth Loops 120 Bpm/Lead_Line_Dm.wav", want{120, "Dm", false, "loop", []string{"synth/lead"}}},
		{"Tech House 124 Gm/Stab_02.wav", want{124, "Gm", false, "oneshot", []string{"synth/stab"}}},
		{"Loops/Vocal_Loops/Vox_Phrase_128_F#.wav", want{128, "F#", false, "loop", []string{"vocals"}}},
		{"Hits/FX/Impact_Big_02.wav", want{0, "", false, "oneshot", []string{"fx/impact"}}},
		{"Keys Loops/Rhodes_Chords_90_Ebmaj.wav", want{90, "D#", false, "loop", []string{"keys/rhodes", "synth/chord"}}},
		{"Loops/Bass/Reese_174_F.wav", want{174, "F", false, "loop", []string{"bass"}}},
		{"Loops/Percussion Loops/Conga_Groove_98.wav", want{98, "", false, "loop", []string{"drums/percussion"}}},
		{"Multis/Piano_C3.wav", want{0, "C", true, "", []string{"keys/piano"}}},
		{"Loops/Guitar/Gtr_Strum_Amin_100bpm.wav", want{100, "Am", false, "loop", []string{"guitar"}}},
		{"Sub_Bass_Hits/Sub_G#.wav", want{0, "G#", false, "oneshot", []string{"bass"}}},
		{"Loops/Pads/Pad_Loop_Warm_G_Minor_110.wav", want{110, "Gm", false, "loop", []string{"synth/pad"}}},
		{"Loops/Strings/Strings_Legato_70_D_minor.wav", want{70, "Dm", false, "loop", []string{"orchestral/strings"}}},

		// --- Cymatics-style: "Cymatics - Name - 140 BPM F# Min" ---
		{"Cymatics - Eternity Melody Loop 1 - 140 BPM F# Min.wav", want{140, "F#m", false, "loop", nil}},
		{"Cymatics - Odyssey 808 - C.wav", want{0, "C", false, "", []string{"bass/808"}}},
		{"Cymatics - Venom Kick 3.wav", want{0, "", false, "", []string{"drums/kick"}}},
		{"Cymatics - Hat Loop 4 - 150 BPM.wav", want{150, "", false, "loop", []string{"drums/hihat"}}},
		{"Cymatics - Lofi Drum Loop 12 - 85 BPM.wav", want{85, "", false, "loop", []string{"drums"}}},
		{"Cymatics - Orchid Guitar Loop 3 - 92 BPM A Min.wav", want{92, "Am", false, "loop", []string{"guitar"}}},
		{"Cymatics - Dark Piano - 130 BPM D# Maj.wav", want{130, "D#", false, "", []string{"keys/piano"}}},
		{"Cymatics - Snare 12 - E.wav", want{0, "", false, "", []string{"drums/snare"}}},
		{"Melodies/Cymatics - Nebula - 160 BPM G Min/Cymatics - Nebula - Stem Bass.wav", want{160, "Gm", false, "", []string{"bass"}}},
		{"Cymatics - Vocal Chant 7 - 100 BPM Bb Min.wav", want{100, "A#m", false, "", []string{"vocals"}}},
		{"Cymatics - Perc Loop 9 - 128 BPM.wav", want{128, "", false, "loop", []string{"drums/percussion"}}},
		{"Cymatics - Riser 3 - 150 BPM.wav", want{150, "", false, "", []string{"fx/riser"}}},
		{"Cymatics - Flute Loop - 75 BPM C Maj.wav", want{75, "C", false, "loop", []string{"orchestral/flute"}}},

		// --- required traps ---
		{"Kick_01.wav", want{0, "", false, "", []string{"drums/kick"}}},
		{"808.wav", want{0, "", false, "", []string{"bass/808"}}},
		{"808_Sub_Long_01.wav", want{0, "", false, "", []string{"bass/808"}}},
		{"A Day In.wav", want{0, "", false, "", nil}},
		{"A_Day_In_The_Life_120.wav", want{120, "", false, "", nil}},
		{"Keys_Gmin7.wav", want{0, "Gm", true, "", []string{"keys"}}},
		{"140_Fsharp_min.wav", want{140, "F#m", false, "", nil}},
		{"Pad_Bb.wav", want{0, "A#", false, "", []string{"synth/pad"}}},
		{"Synth_D_Version.wav", want{0, "", false, "", []string{"synth"}}},
		{"Bass_DB_Boost.wav", want{0, "", false, "", []string{"bass"}}},
		{"Noise_-12db_Sweep.wav", want{0, "", false, "", []string{"fx/noise", "fx/sweep"}}},

		// --- numbers that are not tempos ---
		{"Snare_64.wav", want{0, "", false, "", []string{"drums/snare"}}},
		{"Kick_101.wav", want{0, "", false, "", []string{"drums/kick"}}},
		{"Sine_100hz.wav", want{0, "", false, "", nil}},
		{"Sine_100_Hz.wav", want{0, "", false, "", nil}},
		{"Sweep_120_ms.wav", want{0, "", false, "", []string{"fx/sweep"}}},
		{"Drums_Vol_100_Kit.wav", want{0, "", false, "", []string{"drums"}}},
		{"Pack 2019/Clap_07.wav", want{0, "", false, "", []string{"drums/clap"}}},
		{"TR-909_Ride.wav", want{0, "", false, "", []string{"drums/cymbal"}}},
		{"303_Acid_Line_130_Am.wav", want{130, "Am", false, "", nil}},
		{"Loop_8_bars_128.wav", want{128, "", false, "loop", nil}},
		{"Texture_96_khz.wav", want{0, "", false, "", []string{"fx/texture"}}},
		{"Vox_Take_72.wav", want{0, "", false, "", []string{"vocals"}}},
		{"01_Intro_Pad.wav", want{0, "", false, "", []string{"synth/pad"}}},
		{"Tom_Hi_16.wav", want{0, "", false, "", []string{"drums/tom"}}},
		{"Shaker_Loop_90.wav", want{90, "", false, "loop", []string{"drums/percussion"}}},
		{"Shaker_90.wav", want{0, "", false, "", []string{"drums/percussion"}}},
		{"90_Rhodes_Loop.wav", want{90, "", false, "loop", []string{"keys/rhodes"}}},
		{"Drum_Loop_120bpm_24bit.wav", want{120, "", false, "loop", []string{"drums"}}},
		{"bpm128_arp_loop.wav", want{128, "", false, "loop", []string{"synth/arp"}}},
		{"Lead_Loop_128_BPM_4_Bars.wav", want{128, "", false, "loop", []string{"synth/lead"}}},
		{"Lead_100_128.wav", want{128, "", false, "", []string{"synth/lead"}}},

		// --- key spellings and lookalikes ---
		{"Bass_Loop_120_E.wav", want{120, "E", false, "loop", []string{"bass"}}},
		{"Bass_Loop_E.wav", want{0, "E", true, "loop", []string{"bass"}}},
		{"Kick_120_E.wav", want{120, "", false, "", []string{"drums/kick"}}},
		{"Pluck_C♯m_128.wav", want{128, "C#m", false, "", []string{"synth/pluck"}}},
		{"Pad_E♭.wav", want{0, "D#", false, "", []string{"synth/pad"}}},
		{"arp_loop_f#m_125.wav", want{125, "F#m", false, "loop", []string{"synth/arp"}}},
		{"bass_loop_amin_120.wav", want{120, "Am", false, "loop", []string{"bass"}}},
		{"bass_loop_120_am.wav", want{120, "Am", false, "loop", []string{"bass"}}},
		{"i_am_bass.wav", want{0, "", false, "", []string{"bass"}}},
		{"AM_Radio_FX.wav", want{0, "", false, "", []string{"fx"}}},
		{"FM_Bass_Dm.wav", want{0, "Dm", false, "", []string{"bass"}}},
		{"Organ_Cb_Maj.wav", want{0, "B", false, "", []string{"keys/organ"}}},
		{"Lead_Gb.wav", want{0, "F#", false, "", []string{"synth/lead"}}},
		{"Chord_Cmaj7_Stab.wav", want{0, "C", true, "oneshot", []string{"synth/chord", "synth/stab"}}},
		{"Chords_Am7_90.wav", want{90, "Am", true, "", []string{"synth/chord"}}},
		{"Sax_Lick_Dsharp_minor_95.wav", want{95, "D#m", false, "", []string{"orchestral/sax"}}},
		{"Violin_A_Major.wav", want{0, "A", false, "", []string{"orchestral/strings"}}},
		{"Cello_E_flat_major_60.wav", want{60, "D#", false, "", []string{"orchestral/strings"}}},
		{"Abby_Vocal_Adlib.wav", want{0, "", false, "", []string{"vocals"}}},
		{"Edm_Lead_Big.wav", want{0, "", false, "", []string{"synth/lead"}}},
		{"Bass_A_100.wav", want{100, "A", false, "", []string{"bass"}}},
		{"Dm/Bass_Loop_03.wav", want{0, "Dm", false, "loop", []string{"bass"}}},
		{"Dm/Snare_03.wav", want{0, "", false, "", []string{"drums/snare"}}},
		{"F Minor 140/Loop_E.wav", want{140, "E", true, "loop", nil}},

		// --- filename wins over folders ---
		{"120 BPM/Am/Bass_Loop_128_Cm.wav", want{128, "Cm", false, "loop", []string{"bass"}}},
		{"Loops/Kick_One_Shot.wav", want{0, "", false, "oneshot", []string{"drums/kick"}}},
		{"One Shots/Hat_Loop_128.wav", want{128, "", false, "loop", []string{"drums/hihat"}}},
		{"Drums/Bass_Loop_Am_120.wav", want{120, "Am", false, "loop", []string{"bass", "drums"}}},

		// --- tag extraction details ---
		{"HiHat_Closed_03.wav", want{0, "", false, "", []string{"drums/hihat"}}},
		{"OpenHat12.wav", want{0, "", false, "", []string{"drums/hihat"}}},
		{"KickDrum01.wav", want{0, "", false, "", []string{"drums/kick"}}},
		{"808Bass_F.wav", want{0, "F", false, "", []string{"bass/808"}}},
		{"SFX_Whoosh_Wet.wav", want{0, "", false, "", []string{"fx/whoosh", "processing/wet"}}},
		{"Rimshot_Dry.wav", want{0, "", false, "", []string{"drums/snare", "processing/dry"}}},
		{"Crash_Cymbal_Long.wav", want{0, "", false, "", []string{"drums/cymbal"}}},
		{"Uplifter_Downlifter_Combo.wav", want{0, "", false, "", []string{"fx/riser", "fx/downlifter"}}},
		{"Foley_Atmos_Rain.wav", want{0, "", false, "", []string{"fx/foley", "fx/atmosphere"}}},
		{"Cowbell_Hit.wav", want{0, "", false, "oneshot", []string{"drums/percussion"}}},
		{`Windows\Style\Snap_02.wav`, want{0, "", false, "", []string{"drums/snap"}}},
		{"", want{}},
		{"...", want{}},
	}
	if len(tests) < 80 {
		t.Fatalf("table shrank to %d cases", len(tests))
	}

	failed := 0
	for _, tc := range tests {
		got := ParseName(tc.path)
		w := tc.want
		if got.BPM != w.bpm || got.Key != w.key || got.KeyWeak != w.weak || got.TypeHint != w.typ ||
			!slices.Equal(got.SuggestedTags, w.tags) {
			failed++
			t.Errorf("ParseName(%q)\n  got  bpm=%v key=%q weak=%v type=%q tags=%v\n  want bpm=%v key=%q weak=%v type=%q tags=%v",
				tc.path, got.BPM, got.Key, got.KeyWeak, got.TypeHint, got.SuggestedTags,
				w.bpm, w.key, w.weak, w.typ, w.tags)
		}
	}
	t.Logf("filename table: %d/%d correct", len(tests)-failed, len(tests))
}

// Numbers that are not tempos, and the difference between a tempo the name
// states and one it merely might contain.
func TestParseNameBareNumbers(t *testing.T) {
	tests := []struct {
		path string
		bpm  float64
		weak bool
	}{
		// Hardware model numbers.
		{"Juno-106 Pad C3.wav", 0, false},
		{"SH-101 Bass Pluck.wav", 0, false},
		{"CR-78 Snare.wav", 0, false},
		{"DR-110 Clap.wav", 0, false},
		{"Juno-60 Chord Cmaj7.wav", 0, false},
		{"CS-80 Brass Swell.wav", 0, false},
		{"System-100 Zap FX.wav", 0, false},
		{"Roland Juno-106/Pads/Warm Pad C3.wav", 0, false},
		{"Yamaha DX-100/Bass C2.wav", 0, false},
		// Without the hyphen nothing marks the number as a model; it stays a
		// guess that the audio has to confirm.
		{"MPC 60 Kick.wav", 60, true},
		{"Blink 182 Style Guitar Chug.wav", 182, true},
		{"Route 66 Guitar Lick.wav", 66, true},
		// Multisamples: velocities, MIDI notes, round robins.
		{"Rhodes_F3_100.wav", 100, true},
		{"Piano A2 127.wav", 127, true},
		{"Bass_E1_90.wav", 0, false},
		{"Piano C4 vel 100.wav", 0, false},
		{"Upright_Bass_G2_Vel_110_RR2.wav", 0, false},
		{"MIDI Note 60 Piano.wav", 0, false},
		{"Snare_Top_64.wav", 64, true},
		// Units and charts.
		{"100 Percent Vocal Shout.wav", 0, false},
		{"Vinyl Crackle 78 RPM.wav", 0, false},
		{"Top 100 EDM Kicks/Kick 03.wav", 0, false},
		// A folder number is a guess unless the folder speaks of tempo or
		// loops.
		{"Dubstep 140/Bass_01.wav", 140, true},
		{"Dubstep Loops 140/Bass_01.wav", 140, false},
		{"Dubstep 140 BPM/Bass_01.wav", 140, false},
		{"Tech House 124 Gm/Stab_02.wav", 124, false},
		// A stated folder tempo outranks a guess from the file name.
		{"140 BPM/Synth 128 Dark.wav", 140, false},
		{"Stuff/Synth 128 Dark.wav", 128, true},
		// Hyphens as the only separator are separators.
		{"Deep-House-124-Loop.wav", 124, false},
		{"Loop-120 Am.wav", 120, false},
		// Stated or vouched for.
		{"Bass_120_Am.wav", 120, false},
		{"Chords_Am7_90.wav", 90, false},
		{"Loops/Reese_174.wav", 174, false},
		{"Loops/Kick_120.wav", 0, false},
		{"Loops/Kick 120 Hard.wav", 120, true},
		{"Drum Fill 140 03.wav", 140, false},
		{"Lead_100_128.wav", 128, true},
		{"A_Day_In_The_Life_120.wav", 120, true},
		// Decimal tempos.
		{"Glitch Loop 133.33 BPM.wav", 133.33, false},
		{"Break_172.5bpm.wav", 172.5, false},
		{"bpm 87.5 guitar.wav", 87.5, false},
		{"Loop 133.33.wav", 0, false},
		{"Kick 1.5.wav", 0, false},
	}
	for _, tc := range tests {
		got := ParseName(tc.path)
		if got.BPM != tc.bpm || got.BPMWeak != tc.weak {
			t.Errorf("ParseName(%q): bpm=%v weak=%v, want bpm=%v weak=%v", tc.path, got.BPM, got.BPMWeak, tc.bpm, tc.weak)
		}
	}
}

// Pitch names that are not the key of the audio.
func TestParseNameKeyLookalikes(t *testing.T) {
	tests := []struct {
		path string
		key  string
		weak bool
	}{
		{"Am I Dreaming Vocal 90.wav", "", false},
		{"Am I Dreaming Vocal.wav", "", false},
		{"I Am Lost Vocal.wav", "", false},
		{"Bb Clarinet Staccato.wav", "", false},
		{"Bb Trumpet Stab.wav", "", false},
		{"Eb Alto Sax Lick 02.wav", "", false},
		{"Bb Bass Clarinet Low.wav", "", false},
		{"F Horn Swell 90.wav", "", false},
		{"F French Horn 90.wav", "", false},
		{"A Clarinet Run.wav", "", false},
		// The key of the recording, stated as well, still counts.
		{"Bb Clarinet 120 Bb.wav", "A#", false},
		{"Eb Alto Sax Lick Gm.wav", "Gm", false},
		// A mode makes it a key whatever follows, and so do other words.
		{"Bbm Clarinet Phrase.wav", "A#m", false},
		{"Em Piano Loop 120.wav", "Em", false},
		{"Am Guitar Loop.wav", "Am", false},
		{"Bb Bass Loop 120.wav", "A#", false},
		{"Sax_Loop_Eb_100.wav", "D#", false},
	}
	for _, tc := range tests {
		got := ParseName(tc.path)
		if got.Key != tc.key || got.KeyWeak != tc.weak {
			t.Errorf("ParseName(%q): key=%q weak=%v, want key=%q weak=%v", tc.path, got.Key, got.KeyWeak, tc.key, tc.weak)
		}
	}
}

func TestParseNameFolderFlags(t *testing.T) {
	p := ParseName("Trap 140 BPM Gm/Kick_01.wav")
	if p.BPM != 140 || !p.BPMFromFolder {
		t.Errorf("folder tempo: got bpm=%v fromFolder=%v", p.BPM, p.BPMFromFolder)
	}
	if p.Key != "" {
		t.Errorf("folder key must not apply to a drum hit, got %q", p.Key)
	}
	p = ParseName("Trap 140 BPM Gm/Lead_02.wav")
	if p.Key != "Gm" || !p.KeyFromFolder || p.KeyWeak {
		t.Errorf("folder key: got %q fromFolder=%v weak=%v", p.Key, p.KeyFromFolder, p.KeyWeak)
	}
	if !ParseName("Loops/Top_Loop_01.wav").Drum() || ParseName("Bass_Am.wav").Drum() {
		t.Error("Drum() misreports")
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"Am", "Am", true}, {"A", "A", true}, {"a", "A", true}, {"am", "Am", true},
		{"A minor", "Am", true}, {"a min", "Am", true}, {"AMIN", "Am", true},
		{"Bb", "A#", true}, {"bb", "A#", true}, {"Bbm", "A#m", true}, {"B", "B", true}, {"Bm", "Bm", true},
		{"Db major", "C#", true}, {"C# maj", "C#", true}, {"c#", "C#", true}, {"f#m", "F#m", true},
		{"Gb", "F#", true}, {"Cb", "B", true}, {"Fb", "E", true}, {"E#", "F", true}, {"B#m", "Cm", true},
		{"C♯ minor", "C#m", true}, {"E♭", "D#", true}, {"F sharp minor", "F#m", true}, {"Aflat", "G#", true},
		{"G_min", "Gm", true}, {"D-major", "D", true}, {"FM", "F", true}, {" Em ", "Em", true},
		{"", "", false}, {"H", "", false}, {"Gmin7", "", false}, {"Bass", "", false}, {"12", "", false},
		{"Abby", "", false}, {"minor", "", false},
	}
	for _, tc := range tests {
		got, ok := NormalizeKey(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NormalizeKey(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestKeyRoundTrip(t *testing.T) {
	for pc := range 12 {
		for _, minor := range []bool{false, true} {
			key := formatKey(pc, minor)
			gotPC, gotMinor, ok := splitKey(key)
			if !ok || gotPC != pc || gotMinor != minor {
				t.Errorf("splitKey(%q) = %d, %v, %v", key, gotPC, gotMinor, ok)
			}
			if norm, ok := NormalizeKey(key); !ok || norm != key {
				t.Errorf("NormalizeKey(%q) = %q, %v", key, norm, ok)
			}
		}
	}
	if _, _, ok := splitKey("Bb"); ok {
		t.Error("splitKey accepted a flat")
	}
}
