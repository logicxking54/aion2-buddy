// disabler — turn off every Niagara emitter in a tree of legacy UE5.3 .uasset files.
//
// For each *.uasset under <dir>, find every NiagaraSystem export's "EmitterHandles"
// array and set each handle's bIsEnabled = false (adding the property if the build
// left it at its serialized default). Edits in place. Idempotent.
//
//   dotnet run --project . -- <usmap> <dir>
//
// <usmap> : mappings file for the build (e.g. AION2-5.3.2.0-1.0.0.0.usmap)
// <dir>   : directory of legacy assets produced by `retoc to-legacy`
using System;
using System.IO;
using System.Collections.Generic;
using UAssetAPI;
using UAssetAPI.UnrealTypes;
using UAssetAPI.ExportTypes;
using UAssetAPI.PropertyTypes.Objects;
using UAssetAPI.PropertyTypes.Structs;
using UAssetAPI.Unversioned;

class Program
{
    static int Main(string[] args)
    {
        if (args.Length < 2)
        {
            Console.WriteLine("usage: disabler <usmap> <dir> [-v]");
            return 1;
        }
        string usmapPath = args[0];
        string dir = args[1];
        bool verbose = Array.IndexOf(args, "-v") >= 0;

        Usmap mappings = new Usmap(usmapPath);
        string[] files = Directory.GetFiles(dir, "*.uasset", SearchOption.AllDirectories);

        int edited = 0, totalEmitters = 0, errors = 0;
        foreach (string f in files)
        {
            try
            {
                UAsset asset = new UAsset(f, EngineVersion.VER_UE5_3, mappings);
                int cnt = 0;
                foreach (Export export in asset.Exports)
                    if (export is NormalExport ne)
                        cnt += DisableEmitters(asset, ne.Data);

                if (cnt > 0)
                {
                    asset.Write(f);
                    edited++;
                    totalEmitters += cnt;
                    Console.WriteLine("[OK]  " + Path.GetFileName(f) + " -> disabled " + cnt + " emitter(s)");
                }
                else if (verbose)
                {
                    Console.WriteLine("[--]  " + Path.GetFileName(f) + " (no enabled emitters / not a NiagaraSystem)");
                }
            }
            catch (Exception e)
            {
                errors++;
                Console.WriteLine("[ERR] " + Path.GetFileName(f) + ": " + e.Message);
            }
        }
        Console.WriteLine("==========================================");
        Console.WriteLine("files edited=" + edited + "  emitters disabled=" + totalEmitters + "  errors=" + errors);
        return errors > 0 ? 2 : 0;
    }

    // Set bIsEnabled = false on every EmitterHandles struct element; add the
    // property as false when it isn't serialized. Returns how many it changed.
    static int DisableEmitters(UAsset asset, List<PropertyData> props)
    {
        int count = 0;
        foreach (PropertyData p in props)
        {
            if (p is ArrayPropertyData ap && p.Name.ToString() == "EmitterHandles")
            {
                foreach (PropertyData elem in ap.Value)
                {
                    if (elem is StructPropertyData sp)
                    {
                        BoolPropertyData found = null;
                        foreach (PropertyData sub in sp.Value)
                            if (sub is BoolPropertyData b && sub.Name.ToString() == "bIsEnabled") { found = b; break; }

                        if (found != null)
                        {
                            if (found.Value) { found.Value = false; count++; }
                        }
                        else
                        {
                            BoolPropertyData bp = new BoolPropertyData(new FName(asset, "bIsEnabled"));
                            bp.Value = false;
                            sp.Value.Add(bp);
                            count++;
                        }
                    }
                }
            }
        }
        return count;
    }
}
