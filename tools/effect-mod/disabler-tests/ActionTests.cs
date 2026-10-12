using System.Reflection;
using System.Security.Cryptography;
using System.Text.Json.Nodes;
using UAssetAPI;
using UAssetAPI.ExportTypes;
using UAssetAPI.PropertyTypes.Objects;
using UAssetAPI.PropertyTypes.Structs;
using UAssetAPI.UnrealTypes;
using UAssetAPI.Unversioned;

static class ActionTests
{
    public static void Run(string[] args, Action<bool, string> check)
    {
        var type = Assembly.Load("disabler").GetType("PlayerActionBuild");
        check(type != null, "player-only VFX task gating must exist without weakening the Niagara builder");
        if (type == null) return;
        bool Candidate(string path) => (bool)type.GetMethod("IsCandidate").Invoke(null, new object[] { path });
        const string package = "/Game/FX/Act/Player/01_FX_Common/04_Projectile/Proj_Sorcerer_Skill021_Fx";
        check(Candidate(package), "Flame Arrow projectile is a player VFX candidate");
        foreach (string path in new[] {
            "/Game/Act/Player/01_FX_Common/04_Projectile/Proj_Sorcerer_Skill021",
            package.Replace("Player", "NPC"), package.Replace("Sorcerer", "Cleric"),
            package.Replace("Skill021", "Skill021_TargetMark"), package + "/../Escape",
            "/Game/FX/Act/Player/07_Sorcerer/MB/Sorcerer_Skill021_Range_Fx"
        }) check(!Candidate(path), "must keep gameplay, support, marker, and unknown paths: " + path);
        foreach (string name in new[] { "Gladiator_Fly_Skill032_Fx", "Gladiator_Skill032_Fx" })
            check(!Candidate("/Game/FX/Act/Player/05_Gladiator/GS/" + name), "known fatal parser input must be quarantined before deserialization: " + name);
        if (args.Length != 2) return;
        string root = Path.Combine(Path.GetTempPath(), "aion2-action-tests-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        var map = new Usmap(args[1]);
        string input = SelectionPolicy.SourceFile(args[0], package);
        string Hash(string p) => Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(p)));
        string[] originalHashes = { Hash(input), Hash(Path.ChangeExtension(input, ".uexp")) };
        UAsset Read(string p) => new(p, EngineVersion.VER_UE5_3, map);
        List<StructPropertyData> Links(UAsset a) => ((ArrayPropertyData)((NormalExport)a.Exports[0]).Data.Single(p => p.Name.ToString() == "Sections")).Value
            .Cast<StructPropertyData>().SelectMany(s => ((ArrayPropertyData)s.Value.Single(p => p.Name.ToString() == "TaskLinks")).Value).Cast<StructPropertyData>().ToList();
        bool Enabled(StructPropertyData l) => ((BoolPropertyData)l.Value.Single(p => p.Name.ToString() == "bEnabled")).Value;
        int Patch(string src, string output, string scratch) => (int)type.GetMethod("PatchAsset").Invoke(null, new object[] { src, output, scratch, map });
        var rootLoader = type.GetMethod("LoadFxRoot", BindingFlags.Static | BindingFlags.Public | BindingFlags.NonPublic);
        check(rootLoader != null, "FX loading must parse only the root, never ancillary task payloads");
        if (rootLoader != null)
        {
            string richInput = SelectionPolicy.SourceFile(args[0], "/Game/FX/Act/Player/07_Sorcerer/MB/Sorcerer_Skill021_Fx");
            var opaque = new UAsset(richInput, EngineVersion.VER_UE5_3, map, CustomSerializationFlags.SkipParsingExports | CustomSerializationFlags.SkipPreloadDependencyLoading);
            var loaded = (UAsset)rootLoader.Invoke(null, new object[] { richInput, map });
            check(loaded.Exports.Count > 2 && loaded.Exports.Count(e => e is NormalExport) == 1 &&
                loaded.Exports.Single(e => e is NormalExport).OuterIndex.Index == 0,
                "only the FX root may be parsed even with curves and material tasks present");
            check(loaded.Exports.Where(e => e.OuterIndex.Index != 0).All(e => e is RawExport raw &&
                raw.Data.SequenceEqual(((RawExport)opaque.Exports[loaded.Exports.IndexOf(e)]).Data)),
                "every nonroot export must retain the exact opaque source payload");
            string unchanged = Path.Combine(root, "root-only-unchanged.uasset"); loaded.Write(unchanged);
            check(Hash(richInput) == Hash(unchanged) && Hash(Path.ChangeExtension(richInput, ".uexp")) == Hash(Path.ChangeExtension(unchanged, ".uexp")),
                "root-only parsing must preserve byte-exact unmodified roundtrip");
            string richOutput = Path.Combine(root, "rich-patched", "asset.uasset");
            bool gameplayRejected = false;
            try { Patch(richInput, richOutput, Path.Combine(root, "rich-check")); }
            catch (TargetInvocationException ex) { gameplayRejected = ex.InnerException is InvalidDataException; }
            check(gameplayRejected && !File.Exists(richOutput), "root-only mode must still reject gameplay cues in referenced names");
            string safeRichInput = SelectionPolicy.SourceFile(args[0], "/Game/FX/Act/Player/01_FX_Common/02_Combat/SE_Assassin_Skill001_Cri_01_Fx");
            check(Patch(safeRichInput, richOutput, Path.Combine(root, "rich-safe-check")) > 0,
                "root-only patch must disable visual links while preserving ancillary exports");
        }
        // Use harmless fixture bytes under the quarantined names; the regression must not reproduce a fatal crash.
        foreach (string name in new[] { "Gladiator_Fly_Skill032_Fx", "Gladiator_Skill032_Fx" })
        {
            string quarantined = Path.Combine(root, name + ".uasset");
            File.Copy(input, quarantined); File.Copy(Path.ChangeExtension(input, ".uexp"), Path.ChangeExtension(quarantined, ".uexp"));
            string destination = Path.Combine(root, name + "-output", "asset.uasset"), scratch = Path.Combine(root, name + "-scratch");
            bool rejected = false;
            try { Patch(quarantined, destination, scratch); }
            catch (TargetInvocationException ex) { rejected = ex.InnerException is InvalidDataException; }
            check(rejected && !Directory.Exists(scratch) && !File.Exists(destination), "fatal-input guard must run before the parser or writer: " + name);
        }
        string output = Path.Combine(root, "patched", "projectile.uasset");
        int count = Patch(input, output, Path.Combine(root, "check"));
        check(count == 5, "exactly five Flame Arrow particle task links should be disabled");
        var before = Read(input); var after = Read(output);
        check(Links(before).Count == 5 && Links(before).All(Enabled), "fixture has five initially active visual links");
        check(Links(after).Count == 5 && Links(after).All(l => !Enabled(l)), "all five visual links must read back disabled");
        check(before.Exports.Count == after.Exports.Count && before.Exports.OfType<RawExport>().Count() == 5 &&
            before.Exports.OfType<RawExport>().Zip(after.Exports.OfType<RawExport>()).All(p => p.First.Data.SequenceEqual(p.Second.Data)),
            "every opaque task export must retain its exact original bytes");
        check(originalHashes.SequenceEqual(new[] { Hash(input), Hash(Path.ChangeExtension(input, ".uexp")) }), "patching must not change either source file");
        // Restoring only bEnabled must reconstruct both original files, including timings and references.
        foreach (var link in Links(after)) ((BoolPropertyData)link.Value.Single(p => p.Name.ToString() == "bEnabled")).Value = true;
        string restored = Path.Combine(root, "restored.uasset"); after.Write(restored);
        check(originalHashes.SequenceEqual(new[] { Hash(restored), Hash(Path.ChangeExtension(restored, ".uexp")) }), "only visual link enable flags may change");

        void Reject(UAsset fixture, string label)
        {
            string source = Path.Combine(root, label + ".uasset"); fixture.Write(source);
            string dest = Path.Combine(root, label + "-output", "asset.uasset");
            bool rejected = false;
            try { Patch(source, dest, Path.Combine(root, label + "-check")); }
            catch (TargetInvocationException e) { rejected = e.InnerException is InvalidDataException; }
            check(rejected && !File.Exists(dest), label + " must fail closed without a usable output");
        }
        var wrongType = Read(input);
        ((NormalExport)wrongType.Exports[0]).Data.RemoveAll(p => p.Name.ToString() == "ActAssetType");
        Reject(wrongType, "missing-fx-type");
        var missingFlag = Read(input); Links(missingFlag)[0].Value.RemoveAll(p => p.Name.ToString() == "bEnabled");
        Reject(missingFlag, "missing-enabled-flag");
        var badIndex = Read(input);
        ((ObjectPropertyData)Links(badIndex)[0].Value.Single(p => p.Name.ToString() == "TaskBase")).Value = new FPackageIndex(9999);
        Reject(badIndex, "bad-task-reference");
        var mixed = Read(input);
        int task = ((ObjectPropertyData)Links(mixed)[0].Value.Single(p => p.Name.ToString() == "TaskBase")).Value.Index - 1;
        mixed.Exports[task].ClassIndex = mixed.Exports[0].ClassIndex;
        string mixedInput = Path.Combine(root, "mixed.uasset"); mixed.Write(mixedInput);
        string mixedOutput = Path.Combine(root, "mixed-output", "asset.uasset");
        check(Patch(mixedInput, mixedOutput, Path.Combine(root, "mixed-check")) == 4, "unknown nonvisual task must not be disabled");
        check(Enabled(Links(Read(mixedOutput))[0]), "unknown task link must remain enabled");

        string auditSource = Path.Combine(root, "audit-source");
        string auditInput = SelectionPolicy.SourceFile(auditSource, package);
        Directory.CreateDirectory(Path.GetDirectoryName(auditInput));
        File.Copy(input, auditInput); File.Copy(Path.ChangeExtension(input, ".uexp"), Path.ChangeExtension(auditInput, ".uexp"));
        string store = Path.Combine(root, "store.txt");
        string Row(int id, string path, string links) => $"fixture 000000000000000000000001 {id} ExportBundleData ../../../AION2/Content/{path.Substring(6)}.uasset \"StoreEntry {{ imported_packages: [{links}] }}\"";
        int Run(params string[] command) => SelectiveBuild.Run(command);
        string action = package.Replace("/FX/Act/", "/Act/").Replace("_Fx", "");
        File.WriteAllLines(store, new[] { Row(1, package, ""), Row(2, action, "FPackageId(1)") });
        string manifest = Path.Combine(root, "audit.json");
        check(Run("--audit-actions", auditSource, store, manifest) == 0, "action reference audit accepts an exclusive player call site");
        if (File.Exists(manifest)) check(JsonNode.Parse(File.ReadAllText(manifest))["Approved"].AsArray().Count == 1, "exclusive player FX action should be approved");
        string batchOutput = Path.Combine(root, "batch-output"), batchReport = Path.Combine(root, "batch-report.json");
        check(Run("--player-actions", args[1], manifest, batchOutput, batchReport) == 0, "audited action builder should produce a separate patch tree");
        if (File.Exists(batchReport))
        {
            var report = JsonNode.Parse(File.ReadAllText(batchReport));
            check(report["Written"].GetValue<int>() == 1 && report["DisabledTasks"].GetValue<int>() == 5, "batch report must count the real disabled task links");
            check(!Links(Read(SelectionPolicy.SourceFile(batchOutput, package))).Any(Enabled), "batch cook input must contain disabled flags, not originals");
        }
        check(Run("--player-actions", args[1], manifest, Path.Combine(auditSource, "nested"), Path.Combine(root, "bad-report.json")) != 0 && !Directory.Exists(Path.Combine(auditSource, "nested")), "batch builder must not write below baseline");
        string overlap = Path.Combine(root, "overlap");
        check(Run("--player-actions", args[1], manifest, overlap, Path.Combine(overlap, "report.json")) != 0 && !Directory.Exists(overlap), "reports must stay out of cook input");
        var invalid = JsonNode.Parse(File.ReadAllText(manifest));
        invalid["BlockedByReferences"].AsArray().Add(package);
        string invalidPath = Path.Combine(root, "contradictory.json"); File.WriteAllText(invalidPath, invalid.ToJsonString());
        string invalidOutput = Path.Combine(root, "contradictory-output");
        check(Run("--player-actions", args[1], invalidPath, invalidOutput, Path.Combine(root, "contradictory-report.json")) != 0 && !Directory.Exists(invalidOutput), "a protected asset cannot be injected into an approved batch");
        invalid["BlockedByReferences"].AsArray().Clear(); invalid["Approved"][0]["UassetSha256"] = "changed";
        string stalePath = Path.Combine(root, "stale.json"); File.WriteAllText(stalePath, invalid.ToJsonString());
        string staleOutput = Path.Combine(root, "stale-output");
        check(Run("--player-actions", args[1], stalePath, staleOutput, Path.Combine(root, "stale-report.json")) != 0 && !Directory.Exists(staleOutput), "source hashes must be checked before batch output exists");
        File.AppendAllLines(store, new[] { Row(3, "/Game/Act/Monster/Boss/Attack", "FPackageId(2)") });
        string protectedManifest = Path.Combine(root, "boss-audit.json");
        check(Run("--audit-actions", auditSource, store, protectedManifest) == 0, "boss reference audit should finish");
        if (File.Exists(protectedManifest)) check(JsonNode.Parse(File.ReadAllText(protectedManifest))["Approved"].AsArray().Count == 0, "boss referencing a player action transitively must protect that action's VFX");
        File.WriteAllLines(store, new[] { Row(1, package, ""), Row(2, "/Game/Unknown/Controller", "FPackageId(1)") });
        string unknownManifest = Path.Combine(root, "unknown-audit.json");
        check(Run("--audit-actions", auditSource, store, unknownManifest) == 0, "unknown caller audit should finish");
        if (File.Exists(unknownManifest)) check(JsonNode.Parse(File.ReadAllText(unknownManifest))["Approved"].AsArray().Count == 0, "unknown callers must be kept rather than guessed safe");
        File.WriteAllLines(store, new[] { Row(1, package, ""), Row(2, action, "FPackageId(1)"),
            Row(3, "/Game/FX/Act/Player/01_FX_Common/02_Combat/SE_Chanter_Skill009_Cri_Fx", "FPackageId(2)") });
        string supportManifest = Path.Combine(root, "support-audit.json");
        check(Run("--audit-actions", auditSource, store, supportManifest) == 0, "support-common caller audit finishes");
        check(JsonNode.Parse(File.ReadAllText(supportManifest))["Approved"].AsArray().Count == 0, "a common-directory support action must protect its transitive offensive FX dependency");
        File.WriteAllLines(store, new[] { Row(1, package, ""), Row(2, action, "FPackageId(1)"),
            "fixture 000000000000000000000001 3 ExportBundleData ../../../AION2/Plugins/TestPlugin/Content/Invoker.uasset \"StoreEntry { imported_packages: [FPackageId(2)] }\"" });
        string pluginManifest = Path.Combine(root, "plugin-audit.json");
        check(Run("--audit-actions", auditSource, store, pluginManifest) == 0, "external plugin caller audit finishes");
        check(JsonNode.Parse(File.ReadAllText(pluginManifest))["StorePackages"].GetValue<int>() == 3 &&
            JsonNode.Parse(File.ReadAllText(pluginManifest))["Approved"].AsArray().Count == 0, "unknown plugin caller nodes and their transitive dependencies must not be omitted");
        Console.WriteLine("Action integration evidence: " + root);
    }
}
